package httpapi

import (
	"bytes"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"otklik/internal/domain"
	"otklik/internal/store"
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
	// График динамики и лимит нагрузки — атрибуты дашборда, а не SQL-агрегата.
	if stats.ByDay, err = s.st.AdminDailySeries(r.Context(), from, to, 30); err != nil {
		writeErr(w, r, err)
		return
	}
	if set, err := s.st.GetSettings(r.Context()); err == nil {
		stats.ExpertLimit = set.ExpertActiveLimit
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

// parseExportQuery разбирает фильтры выгрузки: период, статус, приоритет,
// специализация (или free — свободная форма), кризисные да/нет.
func (s *Server) parseExportQuery(r *http.Request) (store.ExportFilter, bool) {
	var f store.ExportFilter
	from, to, err := parsePeriod(r)
	if err != nil {
		return f, false
	}
	f.From, f.To = from, to

	if v := strings.TrimSpace(r.URL.Query().Get("status")); v != "" {
		f.Status = domain.Status(v)
		if !f.Status.Valid() {
			return f, false
		}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("priority")); v != "" {
		if v != "low" && v != "normal" && v != "urgent" {
			return f, false
		}
		f.Priority = v
	}
	if v := strings.TrimSpace(r.URL.Query().Get("group")); v != "" {
		switch v {
		case "free", "psychologists", "conflictologists", "lawyers", "social_pedagogues":
			f.Group = v
		default:
			return f, false
		}
	}
	switch strings.TrimSpace(r.URL.Query().Get("crisis")) {
	case "", "any":
	case "yes", "1", "true":
		b := true
		f.Crisis = &b
	case "no", "0", "false":
		b := false
		f.Crisis = &b
	default:
		return f, false
	}
	return f, true
}

// handleExportAppeals — CSV-выгрузка из личного кабинета с фильтрами.
// Охват зависит от роли: админ — все обращения, оператор — только те, где он
// назначал специалиста или отклонял, специалист — только свои.
func (s *Server) handleExportAppeals(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())

	f, ok := s.parseExportQuery(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorResp{"invalid export filter: check status, priority, group, crisis, from/to"})
		return
	}
	switch p.Role {
	case domain.RoleExpert:
		f.ExpertID = &p.UserID
	case domain.RoleOperator:
		f.OperatorID = &p.UserID
	}

	items, err := s.st.ExportAppeals(r.Context(), f)
	if err != nil {
		writeErr(w, r, err)
		return
	}

	var buf bytes.Buffer
	buf.WriteString("\ufeff") // BOM: Excel корректно открывает UTF-8
	buf.WriteString("id,applicant_type,category,status,priority,crisis,assigned_expert,returns,created_at,updated_at\r\n")
	for _, it := range items {
		rowCSV(&buf,
			it.ID.String(), it.ApplicantType, nullStr(it.Category),
			string(it.Status), it.Priority, boolStr(it.Crisis), nullStr(it.ExpertLogin),
			strconv.Itoa(it.Returns),
			it.CreatedAt.Format("2006-01-02 15:04"), it.UpdatedAt.Format("2006-01-02 15:04"))
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="appeals-`+time.Now().Format("20060102")+`.csv"`)
	_, _ = w.Write(buf.Bytes())
}

// rowCSV пишет одну CSV-строку с экранированием по RFC 4180.
func rowCSV(buf *bytes.Buffer, cols ...string) {
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

func nullStr(s sql.NullString) string {
	if !s.Valid {
		return ""
	}
	return s.String
}

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
