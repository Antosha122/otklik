package httpapi

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"otklik/internal/domain"
	"otklik/internal/store"
)

// staffAppealResp — полная проекция обращения для сотрудников;
// трек-номер (хеш) не раскрывается никому после создания.
type staffAppealResp struct {
	ID                 uuid.UUID            `json:"id"`
	ApplicantType      domain.ApplicantType `json:"applicant_type"`
	CategoryID         *uuid.UUID           `json:"category_id"`
	CategoryName       *string              `json:"category_name"`
	FreeTextMode       bool                 `json:"free_text_mode"`
	Description        string               `json:"description"`
	Status             domain.Status        `json:"status"`
	Priority           domain.Priority      `json:"priority"`
	CrisisDetected     bool                 `json:"crisis_detected"`
	CrisisContact      string               `json:"crisis_contact,omitempty"`
	AssignedExpertID   *uuid.UUID           `json:"assigned_expert_id"`
	AssignedExpert     *string              `json:"assigned_expert"`
	RejectionReason    *string              `json:"rejection_reason,omitempty"`
	Recommendation     *string              `json:"recommendation,omitempty"`
	ReturnCount        int                  `json:"return_count"`
	TransferRequested  bool                 `json:"transfer_requested"`
	Version            int                  `json:"version"`
	CreatedAt          string               `json:"created_at"`
	UpdatedAt          string               `json:"updated_at"`
	IntakeAnswers      []store.IntakeAnswer `json:"intake_answers"`
	Attachments        []store.Attachment   `json:"attachments,omitempty"`
	Participants       []store.Participant  `json:"participants,omitempty"`
	CategorySuggestion *categorySuggestion  `json:"category_suggestion,omitempty"`
	Routing            *routingHint         `json:"routing,omitempty"`
}

type routingExpert struct {
	ID       string `json:"id"`
	Login    string `json:"login"`
	Active   int    `json:"active"`
	Overload bool   `json:"overload"`
}

type routingHint struct {
	Group         string          `json:"group"`
	GroupRU       string          `json:"group_ru"`
	Limit         int             `json:"limit"`
	Experts       []routingExpert `json:"experts"`
	FreeLogin     string          `json:"free_login,omitempty"`
	LeastLoaded   string          `json:"least_loaded,omitempty"`
	AllOverloaded bool            `json:"all_overloaded"`
	NoExperts     bool            `json:"no_experts"`
	Source        string          `json:"source"`
}

var groupRUTitles = map[string]string{
	"psychologists":     "Психологи",
	"conflictologists":  "Конфликтологи",
	"lawyers":           "Юристы",
	"social_pedagogues": "Социальные педагоги",
	"mediators":         "Медиаторы",
}

func groupRU(g string) string {
	if t, ok := groupRUTitles[g]; ok {
		return t
	}
	return g
}

func (s *Server) buildRoutingHint(ctx context.Context, a store.Appeal, sug *categorySuggestion) *routingHint {
	group, source := "", ""
	if a.CategoryID != nil {
		if c, err := s.st.GetCategory(ctx, *a.CategoryID); err == nil {
			group, source = c.SpecialistGroup, "category"
		}
	}
	if group == "" && sug != nil {
		group, source = sug.SpecialistGroup, "suggestion"
	}
	if group == "" {
		return nil
	}

	users, err := s.st.ListUsers(ctx)
	if err != nil {
		return nil
	}
	set, err := s.st.GetSettings(ctx)
	if err != nil {
		return nil
	}
	load, err := s.st.ExpertLoad(ctx)
	if err != nil {
		return nil
	}

	h := routingHint{Group: group, GroupRU: groupRU(group), Limit: set.ExpertActiveLimit, Source: source}
	for _, u := range users {
		if u.Role != string(domain.RoleExpert) || !u.Active || u.SpecialistGroup != group {
			continue
		}
		h.Experts = append(h.Experts, routingExpert{
			ID: u.ID.String(), Login: u.Login,
			Active: load[u.ID], Overload: load[u.ID] >= set.ExpertActiveLimit,
		})
	}
	if len(h.Experts) == 0 {
		h.NoExperts = true
		return &h
	}
	sort.Slice(h.Experts, func(i, j int) bool {
		if h.Experts[i].Active != h.Experts[j].Active {
			return h.Experts[i].Active < h.Experts[j].Active
		}
		return h.Experts[i].Login < h.Experts[j].Login
	})
	h.LeastLoaded = h.Experts[0].Login
	for _, e := range h.Experts {
		if e.Active < h.Limit {
			h.FreeLogin = e.Login
			break
		}
	}
	h.AllOverloaded = h.FreeLogin == ""
	return &h
}

func (s *Server) staffAppeal(r *http.Request, a store.Appeal, p domain.Principal) staffAppealResp {
	resp := staffAppealResp{
		ID: a.ID, ApplicantType: a.ApplicantType,
		CategoryID: a.CategoryID, CategoryName: a.CategoryName,
		FreeTextMode: a.FreeTextMode, Description: a.Description,
		Status: a.Status, Priority: a.Priority, CrisisDetected: a.CrisisDetected,
		AssignedExpertID: a.AssignedExpertID, AssignedExpert: a.AssignedExpert,
		RejectionReason: a.RejectionReason, Recommendation: a.Recommendation,
		ReturnCount: a.ReturnCount, TransferRequested: a.TransferRequested,
		Version:   a.Version,
		CreatedAt: a.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt: a.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
	if p.Role == domain.RoleAdmin {
		resp.Description = ""
	} else {
		resp.IntakeAnswers, _ = s.st.ListIntakeAnswers(r.Context(), a.ID)
		resp.Attachments, _ = s.st.ListAttachments(r.Context(), a.ID)
		if p.Role == domain.RoleOperator {
			resp.CategorySuggestion = s.suggestCategory(r.Context(), a, resp.IntakeAnswers)
			resp.Routing = s.buildRoutingHint(r.Context(), a, resp.CategorySuggestion)
		}
	}
	resp.Participants, _ = s.st.ListParticipants(r.Context(), a.ID)
	// ТЗ «Кризисные обращения», п.4: контакт доступен только оператору
	// и только по кризисным обращениям (эксперт не видит его ни в каком случае).
	if (p.Role == domain.RoleOperator || p.Role == domain.RoleAdmin) && a.CrisisDetected {
		resp.CrisisContact, _ = s.st.GetCrisisContact(r.Context(), a.ID)
	}
	return resp
}

func (s *Server) loadAppealWithAccess(r *http.Request, appealID uuid.UUID, p domain.Principal) (store.Appeal, error) {
	a, err := s.st.GetAppealByID(r.Context(), appealID)
	if err != nil {
		return a, err
	}
	if p.Role == domain.RoleExpert {
		ok, err := s.st.IsParticipant(r.Context(), appealID, p.UserID)
		if err != nil {
			return a, err
		}
		if !ok {
			return a, domain.ErrForbidden
		}
	}
	return a, nil
}

func (s *Server) appealID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := parseUUID(chi.URLParam(r, "appealID"))
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorResp{"appeal id is invalid"})
	}
	return id, ok
}

func (s *Server) handleStaffGetAppeal(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	a, err := s.loadAppealWithAccess(r, id, p)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.staffAppeal(r, a, p))
}

type crisisFlagReq struct {
	Detected *bool  `json:"detected"`
	Reason   string `json:"reason"`
}

// handleSetCrisisFlag — ручное ревью кризисной пометки. Кризисный статус
// видят оператор и эксперт, но подтверждать/снимать пометку по итогам
// разговора с заявителем может только оператор: он ведёт очередь.
func (s *Server) handleSetCrisisFlag(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.Role != domain.RoleOperator {
		writeJSON(w, http.StatusForbidden, errorResp{"crisis flag is available to operator only"})
		return
	}
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	var req crisisFlagReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Detected == nil {
		writeJSON(w, http.StatusBadRequest, errorResp{"detected must be true or false"})
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if len(req.Reason) < 5 {
		writeJSON(w, http.StatusBadRequest, errorResp{"reason is required (min 5 characters)"})
		return
	}
	a, err := s.st.SetCrisisFlag(r.Context(), id, actorPtr(p), p.Role, *req.Detected, req.Reason)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.staffAppeal(r, a, p))
}

func (s *Server) handleOperatorQueue(w http.ResponseWriter, r *http.Request) {
	// Пагинация как у остальных списков: очередь может вырасти до тысяч
	// обращений, отдавать её целиком — недопустимо тяжёлый ответ.
	pq, pg, _, err := parsePageQuery(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{"page and per_page must be positive integers (per_page max 100)"})
		return
	}
	items, totals, err := s.st.ListOperatorQueue(r.Context(), pg)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	body := pagedBody("appeals", items, pq, totals.Total)
	// Счётчик просроченных нужен по всей очереди, а не по странице.
	body["overdue_total"] = totals.Overdue
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleOperatorAppeals(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pq, pg, _, err := parsePageQuery(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{"page and per_page must be positive integers (per_page max 100)"})
		return
	}
	items, total, err := s.st.ListOperatorAppeals(r.Context(), q.Get("status"), pg)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pagedBody("appeals", items, pq, total))
}

func (s *Server) handleExpertAppeals(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	q := r.URL.Query()
	pq, pg, _, err := parsePageQuery(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{"page and per_page must be positive integers (per_page max 100)"})
		return
	}
	items, total, err := s.st.ListExpertAppeals(r.Context(), p.UserID,
		q.Get("status"), q.Get("category"), q.Get("priority"), pg)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pagedBody("appeals", items, pq, total))
}
