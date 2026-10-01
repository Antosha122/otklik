package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"otklik/internal/domain"
)

type expertWithLoad struct {
	ID              string `json:"id"`
	Login           string `json:"login"`
	SpecialistGroup string `json:"specialist_group"`
	ActiveCount     int    `json:"active_count"`
	Limit           int    `json:"limit"`
}

func (s *Server) handleListExperts(w http.ResponseWriter, r *http.Request) {
	users, err := s.st.ListUsers(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	set, err := s.st.GetSettings(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	load, err := s.st.ExpertLoad(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	experts := make([]expertWithLoad, 0, len(users))
	for _, u := range users {
		if u.Role == string(domain.RoleExpert) && u.Active {
			experts = append(experts, expertWithLoad{
				ID: u.ID.String(), Login: u.Login, SpecialistGroup: u.SpecialistGroup,
				ActiveCount: load[u.ID], Limit: set.ExpertActiveLimit,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"experts": experts})
}

func (s *Server) handleAdminGetSettings(w http.ResponseWriter, r *http.Request) {
	set, err := s.st.GetSettings(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, set)
}

type updateSettingsReq struct {
	ExpertActiveLimit *int `json:"expert_active_limit"`
	MaxReturns        *int `json:"max_returns"`
	NoResponseDays    *int `json:"no_response_days"`
}

func (s *Server) handleAdminUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req updateSettingsReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ExpertActiveLimit != nil && (*req.ExpertActiveLimit < 1 || *req.ExpertActiveLimit > 100) {
		writeJSON(w, http.StatusBadRequest, errorResp{"expert_active_limit должен быть от 1 до 100"})
		return
	}
	if req.MaxReturns != nil && (*req.MaxReturns < 0 || *req.MaxReturns > 10) {
		writeJSON(w, http.StatusBadRequest, errorResp{"max_returns должен быть от 0 до 10"})
		return
	}
	if req.NoResponseDays != nil && (*req.NoResponseDays < 1 || *req.NoResponseDays > 90) {
		writeJSON(w, http.StatusBadRequest, errorResp{"no_response_days должен быть от 1 до 90"})
		return
	}
	if req.ExpertActiveLimit != nil {
		if err := s.st.SetExpertActiveLimit(r.Context(), *req.ExpertActiveLimit); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	if req.MaxReturns != nil {
		if err := s.st.SetMaxReturns(r.Context(), *req.MaxReturns); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	if req.NoResponseDays != nil {
		if err := s.st.SetNoResponseDays(r.Context(), *req.NoResponseDays); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	s.handleAdminGetSettings(w, r)
}

func (s *Server) handleAdminAppeals(w http.ResponseWriter, r *http.Request) {
	items, err := s.st.ListAppealsMeta(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"appeals": items})
}

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.st.ListUsers(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

type createUserReq struct {
	Login           string `json:"login"`
	Password        string `json:"password"`
	Role            string `json:"role"`
	SpecialistGroup string `json:"specialist_group"`
}

func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserReq
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Login = strings.TrimSpace(req.Login)
	if len(req.Login) < 3 || len(req.Login) > 50 {
		writeJSON(w, http.StatusBadRequest, errorResp{"login length must be 3..50"})
		return
	}
	if len(req.Password) < 8 {
		writeJSON(w, http.StatusBadRequest, errorResp{"password must be at least 8 characters"})
		return
	}
	switch domain.Role(req.Role) {
	case domain.RoleOperator, domain.RoleExpert, domain.RoleAdmin:
	default:
		writeJSON(w, http.StatusBadRequest, errorResp{"role must be operator, expert or admin"})
		return
	}
	if req.SpecialistGroup != "" {
		switch req.SpecialistGroup {
		case "psychologists", "conflictologists", "lawyers", "social_pedagogues":
		default:
			writeJSON(w, http.StatusBadRequest, errorResp{"specialist_group must be psychologists, conflictologists, lawyers or social_pedagogues"})
			return
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	u, err := s.st.CreateUser(r.Context(), req.Login, string(hash), req.Role, req.SpecialistGroup)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

type patchUserReq struct {
	Role            *string `json:"role"`
	SpecialistGroup *string `json:"specialist_group"`
	Active          *bool   `json:"active"`
}

func (s *Server) handleAdminPatchUser(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseUUID(chi.URLParam(r, "userID"))
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorResp{"user id is invalid"})
		return
	}
	var req patchUserReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Active != nil {
		if err := s.st.SetUserActive(r.Context(), userID, *req.Active); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	if req.Role != nil || req.SpecialistGroup != nil {
		if err := s.st.UpdateUser(r.Context(), userID, req.Role, req.SpecialistGroup, nil); err != nil {
			writeErr(w, r, err)
			return
		}
	}
	u, err := s.st.GetUserByID(r.Context(), userID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleAdminListCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.st.ListCategoriesAll(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": cats})
}

type createCategoryReq struct {
	Name            string `json:"name"`
	SpecialistGroup string `json:"specialist_group"`
	FreeForm        bool   `json:"free_form"`
}

func (s *Server) handleAdminCreateCategory(w http.ResponseWriter, r *http.Request) {
	var req createCategoryReq
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if len(req.Name) < 3 || len(req.Name) > 100 {
		writeJSON(w, http.StatusBadRequest, errorResp{"category name length must be 3..100"})
		return
	}
	switch req.SpecialistGroup {
	case "psychologists", "conflictologists", "lawyers", "social_pedagogues":
	default:
		writeJSON(w, http.StatusBadRequest, errorResp{"specialist_group must be psychologists, conflictologists, lawyers or social_pedagogues"})
		return
	}
	c, err := s.st.CreateCategory(r.Context(), req.Name, req.SpecialistGroup, req.FreeForm)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

type patchCategoryReq struct {
	Name            *string `json:"name"`
	SpecialistGroup *string `json:"specialist_group"`
	Active          *bool   `json:"active"`
}

func (s *Server) handleAdminPatchCategory(w http.ResponseWriter, r *http.Request) {
	catID, ok := parseUUID(chi.URLParam(r, "categoryID"))
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorResp{"category id is invalid"})
		return
	}
	var req patchCategoryReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.st.UpdateCategory(r.Context(), catID, req.Name, req.SpecialistGroup, req.Active); err != nil {
		writeErr(w, r, err)
		return
	}
	c, err := s.st.GetCategory(r.Context(), catID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleAdminComplaints(w http.ResponseWriter, r *http.Request) {
	complaints, err := s.st.ListComplaints(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"complaints": complaints})
}
