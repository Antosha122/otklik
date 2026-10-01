package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"

	"otklik/internal/domain"
)

type QueueItem struct {
	ID                uuid.UUID `json:"id"`
	ApplicantType     string    `json:"applicant_type"`
	CategoryName      *string   `json:"category_name"`
	Status            string    `json:"status"`
	Priority          string    `json:"priority"`
	CrisisDetected    bool      `json:"crisis_detected"`
	TransferRequested bool      `json:"transfer_requested"`
	ReturnCount       int       `json:"return_count"`
	CreatedAt         time.Time `json:"created_at"`
	WaitingSec        int       `json:"waiting_sec"`
	Overdue           bool      `json:"overdue"`
	AttachmentsCount  int       `json:"attachments_count"`
	RoutingGroup      *string   `json:"routing_group,omitempty"`
	NoExpertInGroup   bool      `json:"no_expert_in_group"`
	GroupOverloaded   bool      `json:"group_overloaded"`
}

// routingFlagsSQL — общие SQL-выражения маршрутизации; $N — лимит активных обращений из настроек.
const routingFlagsSQL = `
	       c.specialist_group,
	       c.specialist_group IS NOT NULL AND NOT EXISTS (
	         SELECT 1 FROM users ug
	         WHERE ug.role = 'expert' AND ug.active
	           AND ug.specialist_group = c.specialist_group
	       ),
	       c.specialist_group IS NOT NULL AND EXISTS (
	         SELECT 1 FROM users ug
	         WHERE ug.role = 'expert' AND ug.active
	           AND ug.specialist_group = c.specialist_group
	       ) AND NOT EXISTS (
	         SELECT 1 FROM users uf
	         WHERE uf.role = 'expert' AND uf.active
	           AND uf.specialist_group = c.specialist_group
	           AND (SELECT count(*) FROM appeals ax
	                WHERE ax.assigned_expert_id = uf.id
	                  AND ax.status IN ('assigned', 'in_progress',
	                                    'needs_clarification', 'answer_ready')) < %d
	       )`

func (st *Store) ListOperatorQueue(ctx context.Context) ([]QueueItem, error) {
	set, err := st.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := st.DB.QueryContext(ctx, `
		SELECT a.id, a.applicant_type, c.name, a.status, a.priority, a.crisis_detected,
		       a.transfer_requested, a.return_count, a.created_at,
		       EXTRACT(EPOCH FROM (now() - a.created_at))::int,
		       EXTRACT(EPOCH FROM (now() - a.created_at))::int > $1,
		       (SELECT count(*) FROM attachments at WHERE at.appeal_id = a.id),
		       `+fmt.Sprintf(routingFlagsSQL, set.ExpertActiveLimit)+`
		FROM appeals a LEFT JOIN categories c ON c.id = a.category_id
		WHERE a.status IN ('new', 'returned') OR a.transfer_requested
		ORDER BY a.crisis_detected DESC,
		         (a.priority = 'urgent') DESC,
		         EXTRACT(EPOCH FROM (now() - a.created_at))::int > $1 DESC,
		         a.created_at ASC`,
		domain.QueueOverdueHours*3600)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []QueueItem
	for rows.Next() {
		var it QueueItem
		if err := rows.Scan(&it.ID, &it.ApplicantType, &it.CategoryName, &it.Status,
			&it.Priority, &it.CrisisDetected, &it.TransferRequested, &it.ReturnCount,
			&it.CreatedAt, &it.WaitingSec, &it.Overdue, &it.AttachmentsCount,
			&it.RoutingGroup, &it.NoExpertInGroup, &it.GroupOverloaded); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

type OperatorListItem struct {
	ID                uuid.UUID `json:"id"`
	ApplicantType     string    `json:"applicant_type"`
	CategoryName      *string   `json:"category_name"`
	Status            string    `json:"status"`
	Priority          string    `json:"priority"`
	CrisisDetected    bool      `json:"crisis_detected"`
	TransferRequested bool      `json:"transfer_requested"`
	AssignedExpert    *string   `json:"assigned_expert"`
	ReturnCount       int       `json:"return_count"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	NoReplySec        *int      `json:"no_reply_sec"`
}

// Page — параметры постраничной выдачи списков. Limit == 0 означает
// «без пагинации» (CSV-выгрузка и прочие полные выборки).
type Page struct {
	Limit  int
	Offset int
}

func applyPage(q string, pg Page) string {
	if pg.Limit <= 0 {
		return q
	}
	return fmt.Sprintf("%s LIMIT %d OFFSET %d", q, pg.Limit, pg.Offset)
}

// countRows считает общее число строк выдачи — фронтенду нужны total_pages.
// Исходный запрос используется как подвыборка, ORDER BY внутри допустим.
func (st *Store) countRows(ctx context.Context, q string, args []any) (int, error) {
	var total int
	err := st.DB.QueryRowContext(ctx, "SELECT count(*) FROM ("+q+") _page", args...).Scan(&total)
	return total, err
}

func (st *Store) ListOperatorAppeals(ctx context.Context, status string, pg Page) ([]OperatorListItem, int, error) {
	q := `
		WITH la AS (
			SELECT appeal_id,
			       max(created_at) FILTER (WHERE author_type = 'applicant') AS last_app,
			       max(created_at) FILTER (WHERE author_type <> 'applicant') AS last_staff
			FROM messages GROUP BY appeal_id
		)
		SELECT a.id, a.applicant_type, c.name, a.status, a.priority, a.crisis_detected,
		       a.transfer_requested, u.login, a.return_count, a.created_at, a.updated_at,
		       CASE WHEN a.assigned_expert_id IS NOT NULL
		             AND la.last_app IS NOT NULL
		             AND (la.last_staff IS NULL OR la.last_app > la.last_staff)
		            THEN EXTRACT(EPOCH FROM (now() - la.last_app))::int END
		FROM appeals a
		LEFT JOIN categories c ON c.id = a.category_id
		LEFT JOIN users u ON u.id = a.assigned_expert_id
		LEFT JOIN la ON la.appeal_id = a.id
		WHERE ($1 = '' OR a.status = $1
		       OR ($1 = 'active' AND a.status IN ('new', 'assigned', 'in_progress',
		           'needs_clarification', 'answer_ready', 'returned'))
		       OR ($1 = 'distributed' AND a.assigned_expert_id IS NOT NULL
		           AND a.status IN ('assigned', 'in_progress', 'needs_clarification', 'answer_ready')))
		ORDER BY (EXTRACT(EPOCH FROM (now() - la.last_app))::int > $2) DESC NULLS LAST,
		         a.updated_at DESC`
	args := []any{status, domain.ResponseOverdueHours * 3600}
	total, err := st.countRows(ctx, q, args)
	if err != nil {
		return nil, 0, err
	}
	rows, err := st.DB.QueryContext(ctx, applyPage(q, pg), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []OperatorListItem
	for rows.Next() {
		var it OperatorListItem
		var noReply sql.NullInt64
		if err := rows.Scan(&it.ID, &it.ApplicantType, &it.CategoryName, &it.Status,
			&it.Priority, &it.CrisisDetected, &it.TransferRequested, &it.AssignedExpert,
			&it.ReturnCount, &it.CreatedAt, &it.UpdatedAt, &noReply); err != nil {
			return nil, 0, err
		}
		if noReply.Valid {
			v := int(noReply.Int64)
			it.NoReplySec = &v
		}
		out = append(out, it)
	}
	return out, total, rows.Err()
}

type ExpertListItem struct {
	ID             uuid.UUID `json:"id"`
	ApplicantType  string    `json:"applicant_type"`
	CategoryName   *string   `json:"category_name"`
	Status         string    `json:"status"`
	Priority       string    `json:"priority"`
	CrisisDetected bool      `json:"crisis_detected"`
	ReturnCount    int       `json:"return_count"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (st *Store) ListExpertAppeals(ctx context.Context, expertID uuid.UUID,
	status, category, priority string, pg Page) ([]ExpertListItem, int, error) {
	q := `
		SELECT a.id, a.applicant_type, c.name, a.status, a.priority, a.crisis_detected,
		       a.return_count, a.created_at, a.updated_at
		FROM appeals a
		JOIN appeal_participants p ON p.appeal_id = a.id
		LEFT JOIN categories c ON c.id = a.category_id
		WHERE p.expert_id = $1
		  AND ($2 = '' OR a.status = $2)
		  AND ($3 = '' OR c.name = $3)
		  AND ($4 = '' OR a.priority = $4)
		ORDER BY (a.priority = 'urgent') DESC, a.created_at ASC`
	args := []any{expertID, status, category, priority}
	total, err := st.countRows(ctx, q, args)
	if err != nil {
		return nil, 0, err
	}
	rows, err := st.DB.QueryContext(ctx, applyPage(q, pg), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []ExpertListItem
	for rows.Next() {
		var it ExpertListItem
		if err := rows.Scan(&it.ID, &it.ApplicantType, &it.CategoryName, &it.Status,
			&it.Priority, &it.CrisisDetected, &it.ReturnCount, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, it)
	}
	return out, total, rows.Err()
}

type AdminAppealMeta struct {
	ID               uuid.UUID `json:"id"`
	ApplicantType    string    `json:"applicant_type"`
	CategoryName     *string   `json:"category_name"`
	Status           string    `json:"status"`
	Priority         string    `json:"priority"`
	CrisisDetected   bool      `json:"crisis_detected"`
	HasCrisisContact bool      `json:"has_crisis_contact"`
	ExpertLogin      *string   `json:"assigned_expert"`
	ReturnCount      int       `json:"return_count"`
	Version          int       `json:"version"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	RoutingGroup     *string   `json:"routing_group,omitempty"`
	NoExpertInGroup  bool      `json:"no_expert_in_group"`
	GroupOverloaded  bool      `json:"group_overloaded"`
}

// ListOperatorWorkedAppealsMeta — обращения, с которыми оператор реально
// работал: назначал специалиста (событие 'assign') или сам отклонял
// (событие 'status' → 'rejected'). Обращения, просто прошедшие мимо него
// в общей очереди, оператору не выгружаются.
func (st *Store) ListOperatorWorkedAppealsMeta(ctx context.Context, userID uuid.UUID, pg Page) ([]AdminAppealMeta, int, error) {
	set, err := st.GetSettings(ctx)
	if err != nil {
		return nil, 0, err
	}
	q := `
		SELECT a.id, a.applicant_type, c.name, a.status, a.priority, a.crisis_detected,
		       EXISTS (SELECT 1 FROM crisis_contacts cc WHERE cc.appeal_id = a.id),
		       u.login, a.return_count, a.version, a.created_at, a.updated_at,
		       ` + fmt.Sprintf(routingFlagsSQL, set.ExpertActiveLimit) + `
		FROM appeals a
		LEFT JOIN categories c ON c.id = a.category_id
		LEFT JOIN users u ON u.id = a.assigned_expert_id
		WHERE EXISTS (
			SELECT 1 FROM appeal_events e
			WHERE e.appeal_id = a.id AND e.actor_id = $1
			  AND (e.event_type = 'assign'
			       OR (e.event_type = 'status' AND e.new_value = 'rejected'))
		)
		ORDER BY a.created_at DESC`
	args := []any{userID}
	total, err := st.countRows(ctx, q, args)
	if err != nil {
		return nil, 0, err
	}
	rows, err := st.DB.QueryContext(ctx, applyPage(q, pg), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []AdminAppealMeta
	for rows.Next() {
		var m AdminAppealMeta
		if err := rows.Scan(&m.ID, &m.ApplicantType, &m.CategoryName, &m.Status,
			&m.Priority, &m.CrisisDetected, &m.HasCrisisContact, &m.ExpertLogin,
			&m.ReturnCount, &m.Version, &m.CreatedAt, &m.UpdatedAt,
			&m.RoutingGroup, &m.NoExpertInGroup, &m.GroupOverloaded); err != nil {
			return nil, 0, err
		}
		out = append(out, m)
	}
	return out, total, rows.Err()
}

func (st *Store) ListAppealsMeta(ctx context.Context, pg Page, status string) ([]AdminAppealMeta, int, error) {
	set, err := st.GetSettings(ctx)
	if err != nil {
		return nil, 0, err
	}
	q := `
		SELECT a.id, a.applicant_type, c.name, a.status, a.priority, a.crisis_detected,
		       EXISTS (SELECT 1 FROM crisis_contacts cc WHERE cc.appeal_id = a.id),
		       u.login, a.return_count, a.version, a.created_at, a.updated_at,
		       ` + fmt.Sprintf(routingFlagsSQL, set.ExpertActiveLimit) + `
		FROM appeals a
		LEFT JOIN categories c ON c.id = a.category_id
		LEFT JOIN users u ON u.id = a.assigned_expert_id`
	args := []any{}
	if status != "" {
		q += ` WHERE a.status = $1`
		args = append(args, status)
	}
	q += ` ORDER BY a.created_at DESC`
	total, err := st.countRows(ctx, q, args)
	if err != nil {
		return nil, 0, err
	}
	rows, err := st.DB.QueryContext(ctx, applyPage(q, pg), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []AdminAppealMeta
	for rows.Next() {
		var m AdminAppealMeta
		if err := rows.Scan(&m.ID, &m.ApplicantType, &m.CategoryName, &m.Status,
			&m.Priority, &m.CrisisDetected, &m.HasCrisisContact, &m.ExpertLogin,
			&m.ReturnCount, &m.Version, &m.CreatedAt, &m.UpdatedAt,
			&m.RoutingGroup, &m.NoExpertInGroup, &m.GroupOverloaded); err != nil {
			return nil, 0, err
		}
		out = append(out, m)
	}
	return out, total, rows.Err()
}
