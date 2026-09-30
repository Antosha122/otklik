package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	"otklik/internal/domain"
)

type Appeal struct {
	ID                uuid.UUID
	TrackHash         string
	ApplicantType     domain.ApplicantType
	CategoryID        *uuid.UUID
	CategoryName      *string
	FreeTextMode      bool
	Description       string
	Status            domain.Status
	Priority          domain.Priority
	CrisisDetected    bool
	AssignedExpertID  *uuid.UUID
	AssignedExpert    *string
	RejectionReason   *string
	Recommendation    *string
	ReturnCount       int
	TransferRequested bool
	Version           int
	IdempotencyKey    *string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

const appealCols = `a.id, a.track_hash, a.applicant_type, a.category_id, c.name, a.free_text_mode,
	a.description, a.status, a.priority, a.crisis_detected, a.assigned_expert_id,
	u.login, a.rejection_reason, a.recommendation, a.return_count,
	a.transfer_requested, a.version, a.idempotency_key, a.created_at, a.updated_at`

const appealFrom = ` FROM appeals a
	LEFT JOIN categories c ON c.id = a.category_id
	LEFT JOIN users u ON u.id = a.assigned_expert_id `

func scanAppeal(s interface{ Scan(...any) error }) (Appeal, error) {
	var a Appeal
	err := s.Scan(&a.ID, &a.TrackHash, &a.ApplicantType, &a.CategoryID, &a.CategoryName, &a.FreeTextMode,
		&a.Description, &a.Status, &a.Priority, &a.CrisisDetected, &a.AssignedExpertID,
		&a.AssignedExpert, &a.RejectionReason, &a.Recommendation, &a.ReturnCount,
		&a.TransferRequested, &a.Version, &a.IdempotencyKey, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

func (st *Store) GetAppealByID(ctx context.Context, id uuid.UUID) (Appeal, error) {
	row := st.DB.QueryRowContext(ctx, `SELECT `+appealCols+appealFrom+` WHERE a.id = $1`, id)
	a, err := scanAppeal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return a, domain.ErrNotFound
	}
	return a, err
}

func (st *Store) FindAppealIDByTrackHash(ctx context.Context, hash string) (uuid.UUID, error) {
	var id uuid.UUID
	err := st.DB.QueryRowContext(ctx,
		`SELECT id FROM appeals WHERE track_hash = $1`, hash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, domain.ErrNotFound
	}
	return id, err
}

type CreateAppealParams struct {
	TrackHash      string
	ApplicantType  domain.ApplicantType
	CategoryID     *uuid.UUID
	FreeTextMode   bool
	Description    string
	CrisisDetected bool
	CrisisContact  string
	IdempotencyKey string
	Answers        []IntakeAnswer
}

type IntakeAnswer struct {
	Question string
	Answer   string
}

// CreateAppeal создаёт обращение транзакционно; повтор с тем же idempotency key возвращает существующее.
func (st *Store) CreateAppeal(ctx context.Context, p CreateAppealParams) (Appeal, error) {
	if p.IdempotencyKey != "" {
		var existing uuid.UUID
		err := st.DB.QueryRowContext(ctx,
			`SELECT id FROM appeals WHERE idempotency_key = $1`, p.IdempotencyKey).Scan(&existing)
		if err == nil {
			return st.GetAppealByID(ctx, existing)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return Appeal{}, err
		}
	}

	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		return Appeal{}, err
	}
	defer tx.Rollback()

	var id uuid.UUID
	err = tx.QueryRowContext(ctx, `
		INSERT INTO appeals (track_hash, applicant_type, category_id, free_text_mode,
			description, priority, crisis_detected, idempotency_key)
		VALUES ($1, $2, $3, $4, $5,
			CASE WHEN $6 THEN 'urgent' ELSE 'normal' END,
			$6, NULLIF($7, ''))
		RETURNING id`,
		p.TrackHash, p.ApplicantType, p.CategoryID, p.FreeTextMode,
		p.Description, p.CrisisDetected, p.IdempotencyKey).Scan(&id)
	if err != nil {
		return Appeal{}, err
	}

	for _, ans := range p.Answers {
		if ans.Answer == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO intake_answers (appeal_id, question, answer) VALUES ($1, $2, $3)`,
			id, ans.Question, ans.Answer); err != nil {
			return Appeal{}, err
		}
	}

	if p.CrisisContact != "" {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO crisis_contacts (appeal_id, contact) VALUES ($1, $2)`,
			id, p.CrisisContact); err != nil {
			return Appeal{}, err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO appeal_events (appeal_id, actor_role, event_type, new_value)
		VALUES ($1, 'system', 'status', $2)`, id, string(domain.StatusNew)); err != nil {
		return Appeal{}, err
	}

	if err := tx.Commit(); err != nil {
		return Appeal{}, err
	}
	return st.GetAppealByID(ctx, id)
}

func (st *Store) AppendDescription(ctx context.Context, appealID uuid.UUID,
	addition string, crisisHit bool) (Appeal, error) {
	tag := time.Now().UTC().Format("02.01.2006 15:04")
	var id uuid.UUID
	err := st.DB.QueryRowContext(ctx, `
		UPDATE appeals
		SET description = description || E'\n\n— Дополнение (`+tag+`):\n' || $2,
		    crisis_detected = crisis_detected OR $3,
		    priority = CASE WHEN (crisis_detected OR $3) THEN 'urgent' ELSE priority END,
		    updated_at = now()
		WHERE id = $1 AND status NOT IN ('completed','rejected','closed_no_response')
		RETURNING id`, appealID, addition, crisisHit).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Appeal{}, domain.ErrConflict
	}
	if err != nil {
		return Appeal{}, err
	}
	if _, err := st.DB.ExecContext(ctx,
		`INSERT INTO appeal_events (appeal_id, actor_role, event_type)
		 VALUES ($1, 'applicant', 'append')`, id); err != nil {
		return Appeal{}, err
	}
	return st.GetAppealByID(ctx, id)
}

func (st *Store) ListIntakeAnswers(ctx context.Context, appealID uuid.UUID) ([]IntakeAnswer, error) {
	rows, err := st.DB.QueryContext(ctx,
		`SELECT question, answer FROM intake_answers WHERE appeal_id = $1 ORDER BY question`, appealID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IntakeAnswer
	for rows.Next() {
		var a IntakeAnswer
		if err := rows.Scan(&a.Question, &a.Answer); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
