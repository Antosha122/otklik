package store

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"otklik/internal/domain"
)

// AdminSetStatus — смена статуса в обход машины состояний (разблокировка зависших); причина обязательна.
func (st *Store) AdminSetStatus(ctx context.Context, appealID uuid.UUID,
	actorID *uuid.UUID, actorRole domain.Role, to domain.Status, reason string) (Appeal, error) {
	return st.withAppealLock(ctx, appealID, func(ctx context.Context, tx *sql.Tx, a Appeal) error {
		// Терминальный статус — архив: завершённое/отклонённое/закрытое
		// обращение нельзя вернуть в работу даже администратору.
		if a.Status.Terminal() {
			return domain.ErrValidation
		}
		if a.Status == to {
			return domain.ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE appeals SET status = $2, version = version + 1, updated_at = now()
			WHERE id = $1 AND version = $3`, appealID, string(to), a.Version); err != nil {
			return err
		}
		return addEvent(ctx, tx, EventPayload{
			AppealID: appealID, ActorID: actorID, ActorRole: actorRole,
			EventType: "status", OldValue: string(a.Status), NewValue: string(to), Reason: reason,
		})
	})
}

func (st *Store) ReturnForRework(ctx context.Context, appealID uuid.UUID,
	actorID *uuid.UUID, actorRole domain.Role, reason string) (Appeal, error) {
	return st.withAppealLock(ctx, appealID, func(ctx context.Context, tx *sql.Tx, a Appeal) error {
		if a.Status != domain.StatusAnswerReady {
			return domain.ErrConflict
		}
		if !domain.CanTransition(a.Status, domain.StatusReturned) {
			return domain.ErrValidation
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE appeals SET status = $2, return_count = return_count + 1,
				version = version + 1, updated_at = now()
			WHERE id = $1 AND status = $3 AND version = $4`,
			appealID, string(domain.StatusReturned), string(domain.StatusAnswerReady), a.Version)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return domain.ErrConflict
		}
		return addEvent(ctx, tx, EventPayload{
			AppealID: appealID, ActorID: actorID, ActorRole: actorRole,
			EventType: "status", OldValue: string(a.Status),
			NewValue: string(domain.StatusReturned), Reason: reason,
		})
	})
}

type MyStats struct {
	Role             string   `json:"role"`
	Assigned         int      `json:"assigned"`
	Active           int      `json:"active"`
	Resolved         int      `json:"resolved"`
	Rejected         int      `json:"rejected"`
	Recommendations  int      `json:"recommendations"`
	AvgResolutionHrs *float64 `json:"avg_resolution_hours"`
}

const activeStatusFilter = `status NOT IN ('completed','rejected','closed_no_response')`

func (st *Store) GetMyStats(ctx context.Context, role domain.Role, userID uuid.UUID) (MyStats, error) {
	var s MyStats
	s.Role = string(role)

	if role == domain.RoleOperator {
		if err := st.DB.QueryRowContext(ctx, `
			SELECT count(DISTINCT e.appeal_id) FROM appeal_events e
			WHERE e.actor_id = $1 AND e.event_type = 'assign'`, userID).Scan(&s.Assigned); err != nil {
			return s, err
		}
		if err := st.DB.QueryRowContext(ctx, `
			SELECT count(DISTINCT e.appeal_id)
			FROM appeal_events e JOIN appeals a ON a.id = e.appeal_id
			WHERE e.actor_id = $1 AND e.event_type = 'assign' AND a.`+activeStatusFilter, userID).Scan(&s.Active); err != nil {
			return s, err
		}
		if err := st.DB.QueryRowContext(ctx, `
			SELECT count(DISTINCT e.appeal_id)
			FROM appeal_events e JOIN appeals a ON a.id = e.appeal_id
			WHERE e.actor_id = $1 AND e.event_type = 'assign' AND a.status = 'completed'`, userID).Scan(&s.Resolved); err != nil {
			return s, err
		}
		if err := st.DB.QueryRowContext(ctx, `
			SELECT count(*) FROM appeal_events
			WHERE actor_id = $1 AND event_type = 'status' AND new_value = 'rejected'`, userID).Scan(&s.Rejected); err != nil {
			return s, err
		}
		return s, nil
	}

	if err := st.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM appeals a JOIN appeal_participants p ON p.appeal_id = a.id
		WHERE p.expert_id = $1 AND a.`+activeStatusFilter, userID).Scan(&s.Active); err != nil {
		return s, err
	}
	if err := st.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM appeals a JOIN appeal_participants p ON p.appeal_id = a.id
		WHERE p.expert_id = $1 AND a.status = 'completed'`, userID).Scan(&s.Resolved); err != nil {
		return s, err
	}
	if err := st.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM appeal_events
		WHERE actor_id = $1 AND event_type = 'status' AND new_value = 'answer_ready'`, userID).Scan(&s.Recommendations); err != nil {
		return s, err
	}
	var avg sql.NullFloat64
	if err := st.DB.QueryRowContext(ctx, `
		SELECT avg(extract(epoch from (a.updated_at - a.created_at)) / 3600.0)
		FROM appeals a JOIN appeal_participants p ON p.appeal_id = a.id
		WHERE p.expert_id = $1 AND a.status = 'completed'`, userID).Scan(&avg); err != nil {
		return s, err
	}
	if avg.Valid {
		v := avg.Float64
		s.AvgResolutionHrs = &v
	}
	return s, nil
}

type StatRow struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type WorkloadRow struct {
	Login            string   `json:"login"`
	Role             string   `json:"role"`
	Assigned         int      `json:"assigned"`
	Active           int      `json:"active"`
	Completed        int      `json:"completed"`
	AvgResolutionHrs *float64 `json:"avg_resolution_hours"`
}

type AdminStats struct {
	PeriodFrom          *time.Time    `json:"period_from,omitempty"`
	PeriodTo            *time.Time    `json:"period_to,omitempty"`
	Total               int           `json:"total"`
	Active              int           `json:"active"`
	UrgentActive        int           `json:"urgent_active"`
	Last7Days           int           `json:"last_7_days"`
	Last30Days          int           `json:"last_30_days"`
	Resolved            int           `json:"resolved"`
	UrgentSharePct      float64       `json:"urgent_share_pct"`
	ReturnSharePct      float64       `json:"return_share_pct"`
	AvgResolutionHrs    *float64      `json:"avg_resolution_hours"`
	AvgAssignMin        *float64      `json:"avg_assign_minutes"`
	AvgFirstResponseMin *float64      `json:"avg_first_response_minutes"`
	ByStatus            []StatRow     `json:"by_status"`
	ByCategory          []StatRow     `json:"by_category"`
	ByApplicantType     []StatRow     `json:"by_applicant_type"`
	BySpecialistGroup   []StatRow     `json:"by_specialist_group"`
	Workload            []WorkloadRow `json:"workload"`
	// ByDay и ExpertLimit заполняет хендлер: серия для графика динамики
	// и общий лимит активных обращений (для индикаторов нагрузки).
	ByDay       []DayCount `json:"by_day"`
	ExpertLimit int        `json:"expert_limit"`
}

// periodFilter строит условие по created_at обращения; nil-границы не фильтруют.
func periodFilter(from, to *time.Time, args *[]any) string {
	conds := []string{"TRUE"}
	if from != nil {
		*args = append(*args, *from)
		conds = append(conds, fmt.Sprintf("a.created_at >= $%d", len(*args)))
	}
	if to != nil {
		*args = append(*args, *to)
		conds = append(conds, fmt.Sprintf("a.created_at < $%d", len(*args)))
	}
	return strings.Join(conds, " AND ")
}

func pct(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return math.Round(float64(part)/float64(total)*1000) / 10
}

func nullToPtr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}

func (st *Store) GetAdminStats(ctx context.Context, from, to *time.Time) (AdminStats, error) {
	var s AdminStats
	s.PeriodFrom, s.PeriodTo = from, to

	var urgentTotal, returned int
	var args []any
	if err := st.DB.QueryRowContext(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE a.priority = 'urgent'),
		       count(*) FILTER (WHERE a.return_count > 0)
		FROM appeals a
		WHERE `+periodFilter(from, to, &args), args...).Scan(&s.Total, &urgentTotal, &returned); err != nil {
		return s, err
	}
	s.UrgentSharePct = pct(urgentTotal, s.Total)
	s.ReturnSharePct = pct(returned, s.Total)

	if err := st.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM appeals
		WHERE status NOT IN ('completed','rejected','closed_no_response')`).Scan(&s.Active); err != nil {
		return s, err
	}
	if err := st.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM appeals
		WHERE priority = 'urgent'
		  AND status NOT IN ('completed','rejected','closed_no_response')`).Scan(&s.UrgentActive); err != nil {
		return s, err
	}
	if err := st.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM appeals WHERE created_at > now() - interval '7 days'`).Scan(&s.Last7Days); err != nil {
		return s, err
	}
	if err := st.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM appeals WHERE created_at > now() - interval '30 days'`).Scan(&s.Last30Days); err != nil {
		return s, err
	}

	var avg sql.NullFloat64
	args = nil
	if err := st.DB.QueryRowContext(ctx, `
		SELECT count(*), avg(extract(epoch from (a.updated_at - a.created_at)) / 3600.0)
		FROM appeals a
		WHERE a.status = 'completed' AND `+periodFilter(from, to, &args), args...).Scan(&s.Resolved, &avg); err != nil {
		return s, err
	}
	s.AvgResolutionHrs = nullToPtr(avg)

	args = nil
	if err := st.DB.QueryRowContext(ctx, `
		SELECT avg(extract(epoch from (ea.first_assign - a.created_at)) / 60.0)
		FROM appeals a
		JOIN LATERAL (
		    SELECT min(e.created_at) AS first_assign
		    FROM appeal_events e
		    WHERE e.appeal_id = a.id AND e.event_type = 'assign'
		) ea ON ea.first_assign IS NOT NULL
		WHERE `+periodFilter(from, to, &args), args...).Scan(&avg); err != nil {
		return s, err
	}
	s.AvgAssignMin = nullToPtr(avg)

	args = nil
	if err := st.DB.QueryRowContext(ctx, `
		SELECT avg(extract(epoch from (m.first_reply - a.created_at)) / 60.0)
		FROM appeals a
		JOIN LATERAL (
		    SELECT min(msg.created_at) AS first_reply
		    FROM messages msg
		    WHERE msg.appeal_id = a.id AND msg.author_type IN ('expert','operator')
		) m ON m.first_reply IS NOT NULL
		WHERE `+periodFilter(from, to, &args), args...).Scan(&avg); err != nil {
		return s, err
	}
	s.AvgFirstResponseMin = nullToPtr(avg)

	var err error
	distr := func(field, join string) ([]StatRow, error) {
		args := []any{}
		rows, err := st.DB.QueryContext(ctx, `
			SELECT `+field+`, count(*) AS n
			FROM appeals a
			`+join+`
			WHERE `+periodFilter(from, to, &args)+`
			GROUP BY 1 ORDER BY n DESC`, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []StatRow
		for rows.Next() {
			var r StatRow
			if err := rows.Scan(&r.Label, &r.Count); err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}
	if s.ByStatus, err = distr("a.status", ""); err != nil {
		return s, err
	}
	if s.ByApplicantType, err = distr("a.applicant_type", ""); err != nil {
		return s, err
	}
	if s.BySpecialistGroup, err = distr(
		`CASE WHEN a.free_text_mode THEN 'free' ELSE COALESCE(c.specialist_group, 'free') END`,
		"LEFT JOIN categories c ON c.id = a.category_id"); err != nil {
		return s, err
	}

	args = nil
	rows, err := st.DB.QueryContext(ctx, `
		SELECT COALESCE(c.name, 'Свободная форма'), count(*) AS n
		FROM appeals a LEFT JOIN categories c ON c.id = a.category_id
		WHERE `+periodFilter(from, to, &args)+`
		GROUP BY 1 ORDER BY n DESC`, args...)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var r StatRow
		if err := rows.Scan(&r.Label, &r.Count); err != nil {
			rows.Close()
			return s, err
		}
		s.ByCategory = append(s.ByCategory, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return s, err
	}
	rows.Close()

	// Нагрузка операторов за период — по событиям назначения.
	args = []any{}
	opConds := []string{"u.role = 'operator'", "e.event_type = 'assign'"}
	if from != nil {
		args = append(args, *from)
		opConds = append(opConds, fmt.Sprintf("e.created_at >= $%d", len(args)))
	}
	if to != nil {
		args = append(args, *to)
		opConds = append(opConds, fmt.Sprintf("e.created_at < $%d", len(args)))
	}
	opRows, err := st.DB.QueryContext(ctx, `
		SELECT u.login, u.role, count(DISTINCT e.appeal_id)
		FROM appeal_events e JOIN users u ON u.id = e.actor_id
		WHERE `+strings.Join(opConds, " AND ")+`
		GROUP BY 1,2 ORDER BY 3 DESC`, args...)
	if err != nil {
		return s, err
	}
	for opRows.Next() {
		var w WorkloadRow
		if err := opRows.Scan(&w.Login, &w.Role, &w.Assigned); err != nil {
			opRows.Close()
			return s, err
		}
		s.Workload = append(s.Workload, w)
	}
	if err := opRows.Err(); err != nil {
		opRows.Close()
		return s, err
	}
	opRows.Close()

	// Нагрузка экспертов за период — по ответственности за обращение.
	args = nil
	exRows, err := st.DB.QueryContext(ctx, `
		SELECT u.login, u.role, count(*) AS assigned,
		       count(*) FILTER (WHERE a.`+activeStatusFilter+`),
		       count(*) FILTER (WHERE a.status = 'completed'),
		       avg(extract(epoch from (a.updated_at - a.created_at)) / 3600.0)
		         FILTER (WHERE a.status = 'completed')
		FROM appeal_participants p
		JOIN users u ON u.id = p.expert_id
		JOIN appeals a ON a.id = p.appeal_id
		WHERE u.role = 'expert' AND p.participant_role = 'responsible' AND `+periodFilter(from, to, &args)+`
		GROUP BY 1,2`, args...)
	if err != nil {
		return s, err
	}
	for exRows.Next() {
		var w WorkloadRow
		var exAvg sql.NullFloat64
		if err := exRows.Scan(&w.Login, &w.Role, &w.Assigned, &w.Active, &w.Completed, &exAvg); err != nil {
			exRows.Close()
			return s, err
		}
		w.AvgResolutionHrs = nullToPtr(exAvg)
		s.Workload = append(s.Workload, w)
	}
	if err := exRows.Err(); err != nil {
		exRows.Close()
		return s, err
	}
	exRows.Close()

	sort.SliceStable(s.Workload, func(i, j int) bool { return s.Workload[i].Assigned > s.Workload[j].Assigned })
	return s, nil
}
