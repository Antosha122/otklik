package httpapi

import (
	"net/http"

	"github.com/google/uuid"

	"otklik/internal/domain"
)

func (s *Server) requireResponsible(w http.ResponseWriter, r *http.Request, id uuid.UUID, p domain.Principal) bool {
	if p.Role != domain.RoleExpert {
		writeJSON(w, http.StatusForbidden, errorResp{"this action is available to expert only"})
		return false
	}
	ok, err := s.st.IsResponsible(r.Context(), id, p.UserID)
	if err != nil {
		writeErr(w, r, err)
		return false
	}
	if !ok {
		writeJSON(w, http.StatusForbidden, errorResp{"only the responsible expert can do this"})
		return false
	}
	return true
}

func (s *Server) handleTakeInProgress(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	if !s.requireResponsible(w, r, id, p) {
		return
	}
	a, err := s.st.TransitionStatus(r.Context(), id, actorPtr(p), p.Role,
		domain.StatusAssigned, domain.StatusInProgress, "taken_by_expert")
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.staffAppeal(r, a, p))
}

func (s *Server) handleClarify(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	if !s.requireResponsible(w, r, id, p) {
		return
	}
	a, err := s.st.TransitionStatus(r.Context(), id, actorPtr(p), p.Role,
		domain.StatusInProgress, domain.StatusNeedsClarification, "clarification_requested")
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.staffAppeal(r, a, p))
}

func (s *Server) handlePublishRecommendation(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	if !s.requireResponsible(w, r, id, p) {
		return
	}
	var req recommendationReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Recommendation) < 10 {
		writeJSON(w, http.StatusBadRequest, errorResp{"recommendation is required (min 10 characters)"})
		return
	}
	a, err := s.st.PublishRecommendation(r.Context(), id, actorPtr(p), p.Role, req.Recommendation)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	// Web Push заявителю: статус сменился на «готов ответ» — вернуться за
	// рекомендацией стоит даже тому, кто не следил за чатом.
	s.notifyApplicantAsync(id, "Отклик — специалист подготовил ответ",
		"Откройте обращение, чтобы прочитать рекомендацию")
	writeJSON(w, http.StatusOK, s.staffAppeal(r, a, p))
}

func (s *Server) handleRequestTransfer(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	if !s.requireResponsible(w, r, id, p) {
		return
	}
	var req reasonReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Reason) < 5 {
		writeJSON(w, http.StatusBadRequest, errorResp{"transfer reason is required (min 5 characters)"})
		return
	}
	a, err := s.st.RequestTransfer(r.Context(), id, actorPtr(p), p.Role, req.Reason)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.staffAppeal(r, a, p))
}

func (s *Server) handleCloseNoResponse(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.Role != domain.RoleOperator {
		writeJSON(w, http.StatusForbidden, errorResp{"closing is available to operator only"})
		return
	}
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	a, err := s.st.GetAppealByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if a.Status != domain.StatusNeedsClarification && a.Status != domain.StatusAnswerReady {
		writeJSON(w, http.StatusConflict, errorResp{"can only close from needs_clarification or answer_ready"})
		return
	}
	a, err = s.st.TransitionStatus(r.Context(), id, actorPtr(p), p.Role,
		a.Status, domain.StatusClosedNoResponse, "no_applicant_response")
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.staffAppeal(r, a, p))
}

type contributorReq struct {
	ExpertID string `json:"expert_id"`
}

func (s *Server) handleAddContributor(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	if !s.requireResponsible(w, r, id, p) {
		return
	}
	var req contributorReq
	if !decodeJSON(w, r, &req) {
		return
	}
	expertID, ok := parseUUID(req.ExpertID)
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorResp{"expert_id is invalid"})
		return
	}
	// Нельзя подключить себя: upsert сломал бы роль ответственного.
	if expertID == p.UserID {
		writeJSON(w, http.StatusBadRequest, errorResp{"cannot add yourself as a contributor"})
		return
	}
	expert, err := s.st.GetUserByID(r.Context(), expertID)
	if err != nil || expert.Role != string(domain.RoleExpert) || !expert.Active {
		writeJSON(w, http.StatusBadRequest, errorResp{"expert not found, inactive or not an expert"})
		return
	}
	if err := s.st.AddContributor(r.Context(), id, expertID, actorPtr(p), p.Role); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "contributor added"})
}
