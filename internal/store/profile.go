package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ProfileEvent — запись журнала для личного кабинета: что и когда делал
// сотрудник по обращениям (без содержимого чата — только метаданные).
type ProfileEvent struct {
	AppealID  uuid.UUID `json:"appeal_id"`
	EventType string    `json:"event_type"`
	NewValue  *string   `json:"new_value,omitempty"`
	Reason    *string   `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ListEventsByActor — последние действия сотрудника по всем обращениям,
// свежие сверху. Журнал карточки обращения остаётся полным; здесь — выжимка.
func (st *Store) ListEventsByActor(ctx context.Context, actorID uuid.UUID, limit int) ([]ProfileEvent, error) {
	rows, err := st.DB.QueryContext(ctx, `
		SELECT appeal_id, event_type, new_value, reason, created_at
		FROM appeal_events
		WHERE actor_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, actorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProfileEvent{}
	for rows.Next() {
		var e ProfileEvent
		if err := rows.Scan(&e.AppealID, &e.EventType, &e.NewValue, &e.Reason, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
