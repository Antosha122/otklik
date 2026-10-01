package httpapi

import (
	"net/http"

	"otklik/internal/domain"
)

type assignReq struct {
	ExpertID string `json:"expert_id"`
	Reason   string `json:"reason"`
}

func (s *Server) handleAssign(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.Role != domain.RoleOperator && p.Role != domain.RoleAdmin {
		writeJSON(w, http.StatusForbidden, errorResp{"assign is available to operator and admin only"})
		return
	}
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	var req assignReq
	if !decodeJSON(w, r, &req) {
		return
	}
	expertID, ok := parseUUID(req.ExpertID)
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorResp{"expert_id is invalid"})
		return
	}
	expert, err := s.st.GetUserByID(r.Context(), expertID)
	if err != nil || expert.Role != string(domain.RoleExpert) || !expert.Active {
		writeJSON(w, http.StatusBadRequest, errorResp{"expert not found, inactive or not an expert"})
		return
	}
	a, err := s.st.AssignExpert(r.Context(), id, expertID, actorPtr(p), p.Role, req.Reason)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.staffAppeal(r, a, p))
}

type reasonReq struct {
	Reason string `json:"reason"`
}

func (s *Server) handleReject(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.Role != domain.RoleOperator {
		writeJSON(w, http.StatusForbidden, errorResp{"reject is available to operator only"})
		return
	}
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	var req reasonReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Reason) < 5 {
		writeJSON(w, http.StatusBadRequest, errorResp{"rejection reason is required (min 5 characters)"})
		return
	}
	a, err := s.st.Reject(r.Context(), id, actorPtr(p), p.Role, req.Reason)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.staffAppeal(r, a, p))
}

type recommendationReq struct {
	Recommendation string `json:"recommendation"`
}

func (s *Server) handleCompleteByOperator(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.Role != domain.RoleOperator {
		writeJSON(w, http.StatusForbidden, errorResp{"complete is available to operator only"})
		return
	}
	id, ok := s.appealID(w, r)
	if !ok {
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
	a, err := s.st.CompleteByOperator(r.Context(), id, actorPtr(p), p.Role, req.Recommendation)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.staffAppeal(r, a, p))
}

type priorityReq struct {
	Priority string `json:"priority"`
	Reason   string `json:"reason"`
}

func (s *Server) handleSetPriority(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.Role != domain.RoleOperator && p.Role != domain.RoleAdmin {
		writeJSON(w, http.StatusForbidden, errorResp{"priority change is available to operator and admin only"})
		return
	}
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	var req priorityReq
	if !decodeJSON(w, r, &req) {
		return
	}
	pr := domain.Priority(req.Priority)
	if !pr.Valid() {
		writeJSON(w, http.StatusBadRequest, errorResp{"priority must be low, normal or urgent"})
		return
	}
	a, err := s.st.SetPriority(r.Context(), id, actorPtr(p), p.Role, pr, req.Reason)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.staffAppeal(r, a, p))
}

func (s *Server) handleReturnForRework(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.Role != domain.RoleOperator {
		writeJSON(w, http.StatusForbidden, errorResp{"return for rework is available to operator only"})
		return
	}
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	var req reasonReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Reason) < 5 {
		writeJSON(w, http.StatusBadRequest, errorResp{"return reason is required (min 5 characters)"})
		return
	}
	a, err := s.st.ReturnForRework(r.Context(), id, actorPtr(p), p.Role, req.Reason)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.staffAppeal(r, a, p))
}

type categoryReq struct {
	CategoryID string `json:"category_id"`
	Reason     string `json:"reason"`
}

func (s *Server) handleSetCategory(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.Role != domain.RoleOperator && p.Role != domain.RoleAdmin {
		writeJSON(w, http.StatusForbidden, errorResp{"category change is available to operator and admin only"})
		return
	}
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	var req categoryReq
	if !decodeJSON(w, r, &req) {
		return
	}
	catID, ok := parseUUID(req.CategoryID)
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorResp{"category_id is invalid"})
		return
	}
	if _, err := s.st.GetCategory(r.Context(), catID); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{"category not found"})
		return
	}
	a, err := s.st.SetCategory(r.Context(), id, actorPtr(p), p.Role, catID, req.Reason)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.staffAppeal(r, a, p))
}
