package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"otklik/internal/domain"
	"otklik/internal/store"
)

type applicantAppealResp struct {
	ID                uuid.UUID                 `json:"id"`
	Status            domain.Status             `json:"status"`
	StatusExplanation string                    `json:"status_explanation"`
	ApplicantType     domain.ApplicantType      `json:"applicant_type"`
	Description       string                    `json:"description"`
	CategoryName      *string                   `json:"category_name"`
	Recommendation    *string                   `json:"recommendation,omitempty"`
	ReturnCount       int                       `json:"return_count"`
	MaxReturns        int                       `json:"max_returns"`
	CreatedAt         string                    `json:"created_at"`
	UpdatedAt         string                    `json:"updated_at"`
	IntakeAnswers     []store.IntakeAnswer      `json:"intake_answers"`
	StatusHistory     []store.StatusHistoryItem `json:"status_history"`
	Messages          []store.Message           `json:"messages"`
	Attachments       []store.Attachment        `json:"attachments"`
	CrisisHelp        []domain.CrisisHelp       `json:"crisis_help,omitempty"`
}

func (s *Server) writeApplicantView(w http.ResponseWriter, r *http.Request, appealID uuid.UUID) {
	a, err := s.st.GetAppealByID(r.Context(), appealID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	// Мягкая деградация: обращение показываем даже если соседние выборки
	// упали. Но молча глотать ошибки нельзя — при инциденте (БД на грани,
	// упавший индекс) «пустое» обращение без единого лога не диагностировать.
	// Поэтому каждую неудачную часть логируем с appeal_id.
	var answers []store.IntakeAnswer
	var history []store.StatusHistoryItem
	var messages []store.Message
	var attachments []store.Attachment
	var set store.Settings
	if answers, err = s.st.ListIntakeAnswers(r.Context(), appealID); err != nil {
		slog.Warn("applicant view degraded: intake answers", "appeal_id", appealID, "error", err)
	}
	if history, err = s.st.ListStatusHistoryForApplicant(r.Context(), appealID); err != nil {
		slog.Warn("applicant view degraded: status history", "appeal_id", appealID, "error", err)
	}
	if messages, err = s.st.ListMessages(r.Context(), appealID); err != nil {
		slog.Warn("applicant view degraded: messages", "appeal_id", appealID, "error", err)
	}
	if attachments, err = s.st.ListAttachments(r.Context(), appealID); err != nil {
		slog.Warn("applicant view degraded: attachments", "appeal_id", appealID, "error", err)
	}
	if set, err = s.st.GetSettings(r.Context()); err != nil {
		slog.Warn("applicant view degraded: settings", "appeal_id", appealID, "error", err)
	}

	resp := applicantAppealResp{
		ID: a.ID, Status: a.Status,
		StatusExplanation: a.Status.ExplanationFor(a.ApplicantType),
		ApplicantType:     a.ApplicantType,
		Description:       a.Description,
		CategoryName:      a.CategoryName, Recommendation: a.Recommendation,
		ReturnCount: a.ReturnCount, MaxReturns: set.MaxReturns,
		CreatedAt:     a.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     a.UpdatedAt.UTC().Format(time.RFC3339),
		IntakeAnswers: answers, StatusHistory: history,
		Messages: messages, Attachments: attachments,
	}
	if a.CrisisDetected {
		resp.CrisisHelp = domain.CrisisHelpContacts
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleApplicantView(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	s.writeApplicantView(w, r, p.AppealID)
}

func (s *Server) handleApplicantMessages(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	msgs, err := s.st.ListMessages(r.Context(), p.AppealID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

type postMessageReq struct {
	Text string `json:"text"`
}

func (s *Server) handleApplicantPostMessage(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var req postMessageReq
	if !decodeJSON(w, r, &req) {
		return
	}
	a, err := s.st.GetAppealByID(r.Context(), p.AppealID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if a.Status.Terminal() {
		writeJSON(w, http.StatusConflict, errorResp{"appeal is closed"})
		return
	}
	// Ответ заявителя снимает ожидание уточнения (needs_clarification -> in_progress),
	// чтобы у специалиста пропадала плашка «ожидаем ответа заявителя».
	if a.Status == domain.StatusNeedsClarification {
		if _, err := s.st.TransitionStatus(r.Context(), p.AppealID, nil, domain.RoleApplicant,
			domain.StatusNeedsClarification, domain.StatusInProgress, "applicant_responded"); err != nil &&
			!errors.Is(err, domain.ErrConflict) {
			writeErr(w, r, err)
			return
		}
	}
	if len(strings.TrimSpace(req.Text)) < 1 || len(req.Text) > 4000 {
		writeJSON(w, http.StatusBadRequest, errorResp{"message text length must be 1..4000"})
		return
	}
	msg, err := s.st.CreateMessage(r.Context(), p.AppealID, "applicant", nil, req.Text, "")
	if err != nil {
		writeErr(w, r, err)
		return
	}
	resp := map[string]any{"message": msg}
	// ТЗ «Кризисные обращения», п.1: маркеры в тексте помечают обращение.
	// Если кризис проявился уже в чате — помечаем и сразу показываем помощь.
	if !a.CrisisDetected && s.detectCrisis(r.Context(), strings.TrimSpace(req.Text)) {
		if err := s.st.FlagCrisisFromMessage(r.Context(), p.AppealID); err != nil {
			writeErr(w, r, err)
			return
		}
		resp["crisis_detected"] = true
		resp["crisis_help"] = domain.CrisisHelpContacts
	}
	writeJSON(w, http.StatusCreated, resp)
}

type appendReq struct {
	Text string `json:"text"`
}

func (s *Server) handleApplicantAppend(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var req appendReq
	if !decodeJSON(w, r, &req) {
		return
	}
	text := strings.TrimSpace(req.Text)
	if len(text) < 10 {
		writeJSON(w, http.StatusBadRequest, errorResp{"addition is too short: minimum 10 characters"})
		return
	}
	if len(text) > 4000 {
		writeJSON(w, http.StatusBadRequest, errorResp{"addition is too long (max 4000 characters)"})
		return
	}
	a, err := s.st.AppendDescription(r.Context(), p.AppealID, text, s.detectCrisis(r.Context(), text))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	// Дополнение — тоже ответ заявителя: снимаем ожидание уточнения.
	if a.Status == domain.StatusNeedsClarification {
		a2, err := s.st.TransitionStatus(r.Context(), p.AppealID, nil, domain.RoleApplicant,
			domain.StatusNeedsClarification, domain.StatusInProgress, "applicant_responded")
		if err != nil && !errors.Is(err, domain.ErrConflict) {
			writeErr(w, r, err)
			return
		}
		if err == nil {
			a = a2
		}
	}
	resp := map[string]any{
		"status":             a.Status,
		"status_explanation": a.Status.ExplanationFor(a.ApplicantType),
		"description":        a.Description,
		"crisis_detected":    a.CrisisDetected,
	}
	if a.CrisisDetected {
		resp["crisis_help"] = domain.CrisisHelpContacts
	}
	writeJSON(w, http.StatusOK, resp)
}

type applicantResultReq struct {
	Helped bool   `json:"helped"`
	Reason string `json:"reason"`
}

func (s *Server) handleApplicantResult(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var req applicantResultReq
	if !decodeJSON(w, r, &req) {
		return
	}
	a, err := s.st.ApplicantResult(r.Context(), p.AppealID, req.Helped, req.Reason)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":             a.Status,
		"status_explanation": a.Status.ExplanationFor(a.ApplicantType),
	})
}

type feedbackReq struct {
	Rating  *int    `json:"rating"`
	Comment *string `json:"comment"`
}

func (s *Server) handleApplicantFeedback(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var req feedbackReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Rating != nil && (*req.Rating < 1 || *req.Rating > 5) {
		writeJSON(w, http.StatusBadRequest, errorResp{"rating must be between 1 and 5"})
		return
	}
	if req.Comment != nil && len(*req.Comment) > 2000 {
		writeJSON(w, http.StatusBadRequest, errorResp{"comment is too long"})
		return
	}
	if err := s.st.UpsertFeedback(r.Context(), p.AppealID, req.Rating, req.Comment); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

type complaintReq struct {
	Text string `json:"text"`
}

func (s *Server) handleApplicantComplaint(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var req complaintReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(strings.TrimSpace(req.Text)) < 5 {
		writeJSON(w, http.StatusBadRequest, errorResp{"complaint text is too short"})
		return
	}
	if err := s.st.CreateComplaint(r.Context(), p.AppealID, req.Text); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "accepted"})
}
