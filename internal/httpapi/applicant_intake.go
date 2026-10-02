package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"otklik/internal/domain"
	"otklik/internal/store"
)

func (s *Server) handlePublicCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.st.ListCategoriesPublic(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": cats})
}

type createAppealReq struct {
	ApplicantType  string `json:"applicant_type"`
	CategoryID     string `json:"category_id"`
	FreeText       bool   `json:"free_text"`
	Description    string `json:"description"`
	CrisisContact  string `json:"crisis_contact"`
	IdempotencyKey string `json:"idempotency_key"`
	Answers        []struct {
		Question string `json:"question"`
		Answer   string `json:"answer"`
	} `json:"answers"`
}

type appealCreatedResp struct {
	TrackNumber       string              `json:"track_number"`
	AppealID          string              `json:"appeal_id"`
	Status            domain.Status       `json:"status"`
	StatusExplanation string              `json:"status_explanation"`
	CrisisDetected    bool                `json:"crisis_detected"`
	CrisisHelp        []domain.CrisisHelp `json:"crisis_help,omitempty"`
}

// handleCreateAppeal — анонимное создание обращения; трек-номер выдаётся ровно один раз и является единственным способом доступа заявителя.
func (s *Server) handleCreateAppeal(w http.ResponseWriter, r *http.Request) {
	// 20/час, а не 5: школьники за школьным NAT выходят в интернет с одного IP,
	// и несколько классов с общим адресом не должны целиком упираться в лимит.
	if !s.rl.allow("appeal:"+clientIP(r), 20, time.Hour) {
		writeJSON(w, http.StatusTooManyRequests, errorResp{"too many appeals from this address"})
		return
	}
	var req createAppealReq
	if !decodeJSON(w, r, &req) {
		return
	}

	at := domain.ApplicantType(strings.TrimSpace(req.ApplicantType))
	if !at.Valid() {
		writeJSON(w, http.StatusBadRequest, errorResp{"applicant_type must be schoolchild, parent or teacher"})
		return
	}
	desc := strings.TrimSpace(req.Description)
	if len(desc) < 10 {
		writeJSON(w, http.StatusBadRequest, errorResp{"description is too short: minimum 10 characters"})
		return
	}
	if len(desc) > 8000 {
		writeJSON(w, http.StatusBadRequest, errorResp{"description is too long (max 8000 characters)"})
		return
	}
	req.CrisisContact = strings.TrimSpace(req.CrisisContact)
	if len(req.CrisisContact) > 200 {
		writeJSON(w, http.StatusBadRequest, errorResp{"crisis_contact is too long"})
		return
	}

	params := store.CreateAppealParams{
		TrackHash:      "",
		ApplicantType:  at,
		FreeTextMode:   req.FreeText,
		Description:    desc,
		CrisisContact:  req.CrisisContact,
		IdempotencyKey: strings.TrimSpace(req.IdempotencyKey),
	}
	if len(params.IdempotencyKey) > 100 {
		writeJSON(w, http.StatusBadRequest, errorResp{"idempotency_key is too long"})
		return
	}

	if !req.FreeText {
		catID, ok := parseUUID(req.CategoryID)
		if !ok {
			writeJSON(w, http.StatusBadRequest, errorResp{"category_id is required unless free_text is true"})
			return
		}
		cat, err := s.st.GetCategory(r.Context(), catID)
		if err != nil || !cat.Active {
			writeJSON(w, http.StatusBadRequest, errorResp{"category not found or inactive"})
			return
		}
		params.CategoryID = &catID
		params.FreeTextMode = cat.FreeForm
	}

	texts := []string{desc}
	for _, a := range req.Answers {
		ans := store.IntakeAnswer{Question: a.Question, Answer: strings.TrimSpace(a.Answer)}
		if len(ans.Question) > 500 || len(ans.Answer) > 2000 {
			writeJSON(w, http.StatusBadRequest, errorResp{"intake answer is too long"})
			return
		}
		params.Answers = append(params.Answers, ans)
		texts = append(texts, ans.Answer)
	}
	params.CrisisDetected = domain.DetectCrisis(texts...)

	var trackNumber string
	for i := 0; i < 10; i++ {
		tn, err := domain.GenerateTrackNumber()
		if err != nil {
			writeErr(w, r, err)
			return
		}
		hash := domain.HashTrackWithKey(tn, s.cfg.TrackHmacKey)
		if _, err := s.st.FindAppealIDByTrackHash(r.Context(), hash); err == domain.ErrNotFound {
			trackNumber = tn
			params.TrackHash = hash
			break
		}
	}
	if trackNumber == "" {
		writeErr(w, r, domain.ErrConflict)
		return
	}

	appeal, err := s.st.CreateAppeal(r.Context(), params)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	resp := appealCreatedResp{
		TrackNumber:       trackNumber,
		AppealID:          appeal.ID.String(),
		Status:            appeal.Status,
		StatusExplanation: appeal.Status.ExplanationFor(at),
		CrisisDetected:    appeal.CrisisDetected,
	}
	if appeal.CrisisDetected {
		resp.CrisisHelp = domain.CrisisHelpContacts
	}
	writeJSON(w, http.StatusCreated, resp)
}

type verifyTrackReq struct {
	TrackNumber string `json:"track_number"`
}

// handleVerifyTrack обменивает трек-номер на сессию заявителя; лимит защищает от перебора и расходуется только неверными номерами.
func (s *Server) handleVerifyTrack(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	var req verifyTrackReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.TrackNumber) == "" {
		writeJSON(w, http.StatusBadRequest, errorResp{"track_number is required"})
		return
	}
	appealID, err := s.lookupTrackHash(r.Context(), req.TrackNumber)
	if err != nil {
		// Два лимита: минутный (5/мин) и часовой (20/час). ТЗ 4.7: при превышении —
		// задержка перед следующей попыткой. Задержку делаем на клиенте
		// (Retry-After), а не time.Sleep в горутине: спящий обработчик держит
		// соединение и под ботом сам устраивает себе DoS.
		if !s.rl.allow("trackmin:"+ip, 5, time.Minute) {
			w.Header().Set("Retry-After", "60")
			writeJSON(w, http.StatusTooManyRequests, errorResp{"too many attempts, try later"})
			return
		}
		if !s.rl.allow("track:"+ip, 20, time.Hour) {
			w.Header().Set("Retry-After", "3600")
			writeJSON(w, http.StatusTooManyRequests, errorResp{"too many attempts, try later"})
			return
		}
		// Не раскрываем причину: трек-номер — bearer credential.
		writeJSON(w, http.StatusNotFound, errorResp{"appeal not found"})
		return
	}
	token, tokenHash, err := newToken()
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := s.st.CreateApplicantSession(r.Context(), tokenHash, appealID, time.Now().Add(s.cfg.SessionTTL), applicantUAHash(r)); err != nil {
		writeErr(w, r, err)
		return
	}
	s.rl.reset("track:" + ip)
	s.rl.reset("trackmin:" + ip)
	s.setCookie(w, applicantCookie, token)
	s.writeApplicantView(w, r, appealID)
}

// lookupTrackHash ищет обращение по трек-номеру с учётом ключа: новые обращения
// хешируются HMAC-SHA256 (TRACK_HMAC_KEY), старые — SHA-256 без ключа, поэтому
// при включении ключа на действующей базе оба варианта остаются рабочими.
func (s *Server) lookupTrackHash(ctx context.Context, number string) (uuid.UUID, error) {
	if s.cfg.TrackHmacKey != "" {
		if id, err := s.st.FindAppealIDByTrackHash(ctx, domain.HashTrackWithKey(number, s.cfg.TrackHmacKey)); err == nil {
			return id, nil
		}
	}
	return s.st.FindAppealIDByTrackHash(ctx, domain.HashTrack(number))
}
