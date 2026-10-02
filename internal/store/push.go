package store

import (
	"context"

	"github.com/google/uuid"
)

// PushSubscription — подписка браузера на Web Push, привязанная к обращению.
// Хранится в БД, а не в куках: push-сервер должен найти подписку, даже когда
// заявитель давно закрыл вкладку. Привязка к appeal_id ограничивает доступ:
// отписаться может только владелец обращения (сессия заявителя).
type PushSubscription struct {
	Endpoint string
	P256DH   string
	Auth     string
}

// SavePushSubscription добавляет или обновляет подписку (endpoint уникален).
func (st *Store) SavePushSubscription(ctx context.Context, appealID uuid.UUID, endpoint, p256dh, auth string) error {
	_, err := st.DB.ExecContext(ctx, `
		INSERT INTO push_subscriptions (appeal_id, endpoint, p256dh, auth)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (endpoint) DO UPDATE
		  SET appeal_id = EXCLUDED.appeal_id, p256dh = EXCLUDED.p256dh,
		      auth = EXCLUDED.auth`,
		appealID, endpoint, p256dh, auth)
	return err
}

// PushSubscriptionsForAppeal возвращает подписки заявителя для уведомлений.
func (st *Store) PushSubscriptionsForAppeal(ctx context.Context, appealID uuid.UUID) ([]PushSubscription, error) {
	rows, err := st.DB.QueryContext(ctx,
		`SELECT endpoint, p256dh, auth FROM push_subscriptions WHERE appeal_id = $1`, appealID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushSubscription
	for rows.Next() {
		var s PushSubscription
		if err := rows.Scan(&s.Endpoint, &s.P256DH, &s.Auth); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeletePushSubscription удаляет подписку; appealID обязателен, чтобы
// сессией одного обращения нельзя было отписать чужое.
func (st *Store) DeletePushSubscription(ctx context.Context, appealID uuid.UUID, endpoint string) error {
	_, err := st.DB.ExecContext(ctx,
		`DELETE FROM push_subscriptions WHERE endpoint = $1 AND appeal_id = $2`, endpoint, appealID)
	return err
}
