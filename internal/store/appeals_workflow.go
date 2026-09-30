package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"otklik/internal/domain"
)

type EventPayload struct {
	AppealID  uuid.UUID
	ActorID   *uuid.UUID
	ActorRole domain.Role
	EventType string
	OldValue  string
	NewValue  string
	Reason    string
}

func addEvent(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, e EventPayload) error {
	var actorID uuid.UUID
	if e.ActorID != nil {
		actorID = *e.ActorID
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO appeal_events (appeal_id, actor_id, actor_role, event_type, old_value, new_value, reason)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, ''))`,
		e.AppealID, actorID, string(e.ActorRole),
		e.EventType, e.OldValue, e.NewValue, e.Reason)
	return err
}

func (st *Store) withAppealLock(ctx context.Context, appealID uuid.UUID,
	fn func(ctx context.Context, tx *sql.Tx, a Appeal) error) (Appeal, error) {
	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		return Appeal{}, err
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx,
		`SELECT `+appealCols+appealFrom+` WHERE a.id = $1 FOR UPDATE OF a`, appealID)
	a, err := scanAppeal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Appeal{}, domain.ErrNotFound
	}
	if err != nil {
		return Appeal{}, err
	}
	if err := fn(ctx, tx, a); err != nil {
		return Appeal{}, err
	}
	if err := tx.Commit(); err != nil {
		return Appeal{}, err
	}
	return st.GetAppealByID(ctx, appealID)
}

func (st *Store) TransitionStatus(ctx context.Context, appealID uuid.UUID,
	actorID *uuid.UUID, actorRole domain.Role, expectedFrom domain.Status,
	to domain.Status, reason string) (Appeal, error) {
	return st.withAppealLock(ctx, appealID, func(ctx context.Context, tx *sql.Tx, a Appeal) error {
		if a.Status != expectedFrom {
			return domain.ErrConflict
		}
		if !domain.CanTransition(a.Status, to) {
			return domain.ErrValidation
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE appeals SET status = $2, version = version + 1, updated_at = now()
			WHERE id = $1 AND status = $3 AND version = $4`,
			appealID, string(to), string(expectedFrom), a.Version)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return domain.ErrConflict
		}
		return addEvent(ctx, tx, EventPayload{
			AppealID: appealID, ActorID: actorID, ActorRole: actorRole,
			EventType: "status", OldValue: string(a.Status), NewValue: string(to), Reason: reason,
		})
	})
}

// FlagCrisisFromMessage помечает обращение кризисным, если маркеры впервые
// появились в сообщении заявителя (ТЗ «Кризисные обращения», п.1: маркеры в тексте).
// Идемпотентно: повторный вызов для уже кризисного обращения ничего не меняет.
func (st *Store) FlagCrisisFromMessage(ctx context.Context, appealID uuid.UUID) error {
	_, err := st.withAppealLock(ctx, appealID, func(ctx context.Context, tx *sql.Tx, a Appeal) error {
		if a.CrisisDetected {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE appeals SET crisis_detected = TRUE,
				priority = 'urgent',
				version = version + 1, updated_at = now()
			WHERE id = $1 AND NOT crisis_detected`, appealID); err != nil {
			return err
		}
		return addEvent(ctx, tx, EventPayload{
			AppealID: appealID, ActorRole: domain.Role("system"),
			EventType: "crisis", OldValue: "false", NewValue: "true",
			Reason: "crisis_markers_in_applicant_message",
		})
	})
	return err
}

func (st *Store) SetPriority(ctx context.Context, appealID uuid.UUID,
	actorID *uuid.UUID, actorRole domain.Role, p domain.Priority, reason string) (Appeal, error) {
	return st.withAppealLock(ctx, appealID, func(ctx context.Context, tx *sql.Tx, a Appeal) error {
		if a.Status.Terminal() {
			return domain.ErrConflict
		}
		// Кризисное обращение всегда срочное: понижать приоритет нельзя (ТЗ «Кризисные обращения»).
		if a.CrisisDetected && p != domain.PriorityUrgent {
			return domain.ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE appeals SET priority = $2, version = version + 1, updated_at = now() WHERE id = $1`,
			appealID, string(p)); err != nil {
			return err
		}
		return addEvent(ctx, tx, EventPayload{
			AppealID: appealID, ActorID: actorID, ActorRole: actorRole,
			EventType: "priority", OldValue: string(a.Priority), NewValue: string(p), Reason: reason,
		})
	})
}

func (st *Store) SetCategory(ctx context.Context, appealID uuid.UUID,
	actorID *uuid.UUID, actorRole domain.Role, catID uuid.UUID, reason string) (Appeal, error) {
	return st.withAppealLock(ctx, appealID, func(ctx context.Context, tx *sql.Tx, a Appeal) error {
		if _, err := tx.ExecContext(ctx, `
			UPDATE appeals SET category_id = $2, free_text_mode = FALSE,
				version = version + 1, updated_at = now() WHERE id = $1`,
			appealID, catID); err != nil {
			return err
		}
		old := ""
		if a.CategoryID != nil {
			old = a.CategoryID.String()
		}
		return addEvent(ctx, tx, EventPayload{
			AppealID: appealID, ActorID: actorID, ActorRole: actorRole,
			EventType: "category", OldValue: old, NewValue: catID.String(), Reason: reason,
		})
	})
}
