package httpapi

import (
	"net/http"
	"strings"

	"otklik/internal/domain"
)

func (s *Server) handleStaffMessages(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.Role == domain.RoleOperator || p.Role == domain.RoleAdmin {
		writeJSON(w, http.StatusForbidden, errorResp{"chat is available to expert only"})
		return
	}
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	if _, err := s.loadAppealWithAccess(r, id, p); err != nil {
		writeErr(w, r, err)
		return
	}
	msgs, err := s.st.ListMessages(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

func (s *Server) handleStaffPostMessage(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.Role == domain.RoleOperator || p.Role == domain.RoleAdmin {
		writeJSON(w, http.StatusForbidden, errorResp{"chat is available to expert only"})
		return
	}
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	a, err := s.loadAppealWithAccess(r, id, p)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if a.Status.Terminal() {
		writeJSON(w, http.StatusConflict, errorResp{"appeal is closed"})
		return
	}
	var req postMessageReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(strings.TrimSpace(req.Text)) < 1 || len(req.Text) > 4000 {
		writeJSON(w, http.StatusBadRequest, errorResp{"message text length must be 1..4000"})
		return
	}
	authorType := "operator"
	if p.Role == domain.RoleExpert {
		authorType = "expert"
	}
	msg, err := s.st.CreateMessage(r.Context(), id, authorType, actorPtr(p), req.Text, "")
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, msg)
}

type noteReq struct {
	Text string `json:"text"`
}

func (s *Server) handleStaffNotes(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	// Заметки — служебный инструмент эксперта: оператор и администратор
	// не читают их и на уровне API.
	if p.Role != domain.RoleExpert {
		writeJSON(w, http.StatusForbidden, errorResp{"notes are available to expert only"})
		return
	}
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	if _, err := s.loadAppealWithAccess(r, id, p); err != nil {
		writeErr(w, r, err)
		return
	}
	notes, err := s.st.ListNotes(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": notes})
}

func (s *Server) handleStaffPostNote(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	if p.Role != domain.RoleExpert {
		writeJSON(w, http.StatusForbidden, errorResp{"notes are available to expert only"})
		return
	}
	if _, err := s.loadAppealWithAccess(r, id, p); err != nil {
		writeErr(w, r, err)
		return
	}
	var req noteReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(strings.TrimSpace(req.Text)) < 1 || len(req.Text) > 4000 {
		writeJSON(w, http.StatusBadRequest, errorResp{"note text length must be 1..4000"})
		return
	}
	note, err := s.st.CreateNote(r.Context(), id, p.UserID, req.Text)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, note)
}

func (s *Server) handleStaffEvents(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.Role != domain.RoleOperator && p.Role != domain.RoleAdmin {
		writeJSON(w, http.StatusForbidden, errorResp{"events are available to operator and admin only"})
		return
	}
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	events, err := s.st.ListEvents(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}
