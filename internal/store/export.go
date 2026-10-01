package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"otklik/internal/domain"
)

// ExportFilter — параметры CSV-выгрузки из личного кабинета.
// Пустые значения не фильтруют; ролевой охват задают ExpertID/OperatorID.
type ExportFilter struct {
	From, To   *time.Time
	Status     domain.Status // "" — любой
	Priority   string        // "" — любой
	Group      string        // "" — любая; "free" — свободная форма
	Crisis     *bool         // nil — все
	ExpertID   *uuid.UUID    // роль expert: только его обращения
	OperatorID *uuid.UUID    // роль operator: только где он назначал/отклонял
}

// ExportRow — строка CSV: метаданные обращения без текстов и чатов.
type ExportRow struct {
	ID            uuid.UUID
	ApplicantType string
	Category      sql.NullString
	Status        domain.Status
	Priority      string
	Crisis        bool
	ExpertLogin   sql.NullString
	Returns       int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// exportRowsLimit защищает от гигантских выгрузок: больше строк — уточните фильтры.
const exportRowsLimit = 20000

// ExportAppeals выбирает метаданные обращений под фильтры и ролевой охват.
// Порядок — свежие сверху; лимит строк — exportRowsLimit.
func (st *Store) ExportAppeals(ctx context.Context, f ExportFilter) ([]ExportRow, error) {
	conds := []string{"TRUE"}
	var args []any
	add := func(cond string, val any) {
		args = append(args, val)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	if f.From != nil {
		add("a.created_at >= $%d", *f.From)
	}
	if f.To != nil {
		add("a.created_at < $%d", *f.To)
	}
	if f.Status != "" {
		add("a.status = $%d", string(f.Status))
	}
	if f.Priority != "" {
		add("a.priority = $%d", f.Priority)
	}
	if f.Group != "" {
		if f.Group == "free" {
			conds = append(conds, "a.free_text_mode")
		} else {
			add("COALESCE(c.specialist_group, '') = $%d", f.Group)
		}
	}
	if f.Crisis != nil {
		add("a.crisis_detected = $%d", *f.Crisis)
	}
	if f.ExpertID != nil {
		add(`EXISTS (SELECT 1 FROM appeal_participants p
			WHERE p.appeal_id = a.id AND p.expert_id = $%d)`, *f.ExpertID)
	}
	if f.OperatorID != nil {
		add(`EXISTS (SELECT 1 FROM appeal_events e
			WHERE e.appeal_id = a.id AND e.actor_id = $%d
			  AND (e.event_type = 'assign'
			       OR (e.event_type = 'status' AND e.new_value = 'rejected')))`, *f.OperatorID)
	}
	args = append(args, exportRowsLimit)
	q := `
		SELECT a.id, a.applicant_type, c.name, a.status, a.priority, a.crisis_detected,
		       u.login, a.return_count, a.created_at, a.updated_at
		FROM appeals a
		LEFT JOIN categories c ON c.id = a.category_id
		LEFT JOIN users u ON u.id = a.assigned_expert_id
		WHERE ` + strings.Join(conds, " AND ") + `
		ORDER BY a.created_at DESC
		LIMIT $` + fmt.Sprint(len(args))

	rows, err := st.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExportRow
	for rows.Next() {
		var x ExportRow
		var status string
		if err := rows.Scan(&x.ID, &x.ApplicantType, &x.Category, &status, &x.Priority,
			&x.Crisis, &x.ExpertLogin, &x.Returns, &x.CreatedAt, &x.UpdatedAt); err != nil {
			return nil, err
		}
		x.Status = domain.Status(status)
		out = append(out, x)
	}
	return out, rows.Err()
}
