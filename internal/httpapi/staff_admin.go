package httpapi

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"otklik/internal/domain"
)

type adminStatusReq struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// Машина состояний сознательно не применяется — смысл действия в разблокировке.
func (s *Server) handleAdminSetStatus(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.Role != domain.RoleAdmin {
		writeJSON(w, http.StatusForbidden, errorResp{"status override is available to admin only"})
		return
	}
	id, ok := s.appealID(w, r)
	if !ok {
		return
	}
	var req adminStatusReq
	if !decodeJSON(w, r, &req) {
		return
	}
	to := domain.Status(strings.TrimSpace(req.Status))
	if !to.Valid() {
		writeJSON(w, http.StatusBadRequest, errorResp{"status is invalid"})
		return
	}
	// Админ только «разблокирует» зависшие обращения: закрывать их
	// (завершено / отклонено / закрыто без ответа) он не должен.
	if to.Terminal() {
		writeJSON(w, http.StatusBadRequest, errorResp{"terminal status is not allowed: completed, rejected and closed_no_response are set by the process, not by admin"})
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if len(req.Reason) < 5 {
		writeJSON(w, http.StatusBadRequest, errorResp{"reason is required (min 5 characters)"})
		return
	}
	// Терминальный статус — архив: из completed/rejected/closed_no_response
	// обращение нельзя вернуть в работу. Хендлер даёт понятный ответ,
	// а защита от гонок — повторная проверка внутри транзакции в сторе.
	cur, err := s.st.GetAppealByID(r.Context(), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if cur.Status.Terminal() {
		writeJSON(w, http.StatusBadRequest, errorResp{"appeal is already completed/rejected/closed_no_response — it is archived and its status cannot be changed"})
		return
	}
	a, err := s.st.AdminSetStatus(r.Context(), id, actorPtr(p), p.Role, to, req.Reason)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.staffAppeal(r, a, p))
}

// parsePeriod разбирает параметры from/to (YYYY-MM-DD или RFC3339);
// для даты без времени «до» означает конец этого дня.
func parsePeriod(r *http.Request) (*time.Time, *time.Time, error) {
	parse := func(v string) (*time.Time, bool, error) {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil, false, nil
		}
		if t, err := time.Parse(time.DateOnly, v); err == nil {
			return &t, true, nil
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return nil, false, fmt.Errorf("invalid date %q: use YYYY-MM-DD or RFC3339", v)
		}
		return &t, false, nil
	}
	from, _, err := parse(r.URL.Query().Get("from"))
	if err != nil {
		return nil, nil, err
	}
	to, toDay, err := parse(r.URL.Query().Get("to"))
	if err != nil {
		return nil, nil, err
	}
	if to != nil && toDay {
		end := to.Add(24 * time.Hour)
		to = &end
	}
	if from != nil && to != nil && !to.After(*from) {
		return nil, nil, fmt.Errorf("'to' must be after 'from'")
	}
	return from, to, nil
}

func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	from, to, err := parsePeriod(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{err.Error()})
		return
	}
	stats, err := s.st.GetAdminStats(r.Context(), from, to)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleMyStats(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.Role != domain.RoleOperator && p.Role != domain.RoleExpert {
		writeJSON(w, http.StatusForbidden, errorResp{"personal stats are available to operator and expert only"})
		return
	}
	stats, err := s.st.GetMyStats(r.Context(), p.Role, p.UserID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleExportAppeals(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())

	var buf bytes.Buffer
	buf.WriteString("\ufeff") // BOM: Excel корректно открывает UTF-8
	buf.WriteString("id,applicant_type,category,status,priority,crisis,assigned_expert,returns,created_at,updated_at\r\n")

	row := func(cols ...string) {
		for i, c := range cols {
			if strings.ContainsAny(c, ",\"\r\n") {
				c = `"` + strings.ReplaceAll(c, `"`, `""`) + `"`
			}
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.WriteString(c)
		}
		buf.WriteString("\r\n")
	}

	if p.Role == domain.RoleExpert {
		items, err := s.st.ListExpertAppeals(r.Context(), p.UserID, "", "", "")
		if err != nil {
			writeErr(w, r, err)
			return
		}
		for _, it := range items {
			row(it.ID.String(), it.ApplicantType, deref(it.CategoryName),
				it.Status, it.Priority, boolStr(it.CrisisDetected), "",
				strconv.Itoa(it.ReturnCount), it.CreatedAt.Format("2006-01-02 15:04"), it.UpdatedAt.Format("2006-01-02 15:04"))
		}
	} else if p.Role == domain.RoleOperator {
		// Оператор выгружает только обращения, с которыми работал сам:
		// назначал специалиста или отклонял, — а не весь массив программы.
		items, err := s.st.ListOperatorWorkedAppealsMeta(r.Context(), p.UserID)
		if err != nil {
			writeErr(w, r, err)
			return
		}
		for _, it := range items {
			row(it.ID.String(), it.ApplicantType, deref(it.CategoryName), it.Status, it.Priority,
				boolStr(it.CrisisDetected), deref(it.ExpertLogin),
				strconv.Itoa(it.ReturnCount), it.CreatedAt.Format("2006-01-02 15:04"), it.UpdatedAt.Format("2006-01-02 15:04"))
		}
	} else {
		items, err := s.st.ListAppealsMeta(r.Context())
		if err != nil {
			writeErr(w, r, err)
			return
		}
		for _, it := range items {
			row(it.ID.String(), it.ApplicantType, deref(it.CategoryName),
				it.Status, it.Priority, boolStr(it.CrisisDetected), deref(it.ExpertLogin),
				strconv.Itoa(it.ReturnCount), it.CreatedAt.Format("2006-01-02 15:04"), it.UpdatedAt.Format("2006-01-02 15:04"))
		}
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="appeals.csv"`)
	_, _ = w.Write(buf.Bytes())
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
