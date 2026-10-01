package httpapi

import (
	"net/http"

	"otklik/internal/domain"
)

// handleProfile — личный кабинет сотрудника: карточка профиля, персональная
// аналитика (оператор/эксперт), нагрузка относительно лимита (эксперт)
// и журнал последних действий. Администратору — карточка и журнал.
func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFrom(r.Context())
	if !ok || !p.IsStaff() {
		writeJSON(w, http.StatusUnauthorized, errorResp{"staff authentication required"})
		return
	}
	u, err := s.st.GetUserByID(r.Context(), p.UserID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	resp := map[string]any{
		"user": map[string]any{
			"login":            u.Login,
			"role":             u.Role,
			"specialist_group": u.SpecialistGroup,
			"active":           u.Active,
			"created_at":       u.CreatedAt,
		},
	}

	if p.Role == domain.RoleOperator || p.Role == domain.RoleExpert {
		stats, err := s.st.GetMyStats(r.Context(), p.Role, p.UserID)
		if err != nil {
			writeErr(w, r, err)
			return
		}
		resp["stats"] = stats
	}

	if p.Role == domain.RoleExpert {
		load, err := s.st.ExpertLoad(r.Context())
		if err != nil {
			writeErr(w, r, err)
			return
		}
		set, err := s.st.GetSettings(r.Context())
		if err != nil {
			writeErr(w, r, err)
			return
		}
		active := load[p.UserID]
		resp["workload"] = map[string]any{
			"active":   active,
			"limit":    set.ExpertActiveLimit,
			"overload": active >= set.ExpertActiveLimit,
		}
	}

	events, err := s.st.ListEventsByActor(r.Context(), p.UserID, 10)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	resp["recent_events"] = events // ListEventsByActor возвращает не-nil пустой срез
	writeJSON(w, http.StatusOK, resp)
}
