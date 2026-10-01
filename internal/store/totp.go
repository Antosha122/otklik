package store

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
)

// TOTP-секреты сотрудников (RFC 6238). Секрет пишется на этапе «настройки»
// с enabled=false: пользователь добавляет его в приложение-аутентификатор
// и подтверждает действующим кодом — только тогда включается проверка
// при логине. Хранится в открытом виде: доступ к БД и так даёт управление
// сессиями, шифрование секрета добавило бы ключ туда же.

// SetTOTPSecret сохраняет (или перезаписывает) секрет пользователя —
// не включённым. Вызывается из POST /api/auth/totp/setup.
func (st *Store) SetTOTPSecret(ctx context.Context, userID uuid.UUID, secret string) error {
	_, err := st.DB.ExecContext(ctx, `
		INSERT INTO user_totp (user_id, secret, enabled) VALUES ($1, $2, false)
		ON CONFLICT (user_id) DO UPDATE SET secret = EXCLUDED.secret, enabled = false, created_at = now()`,
		userID, secret)
	return err
}

// EnableTOTP включает проверку кода при логине (секрет уже настроен).
// Возвращает число изменённых строк: 0 — секрет не был настроен.
func (st *Store) EnableTOTP(ctx context.Context, userID uuid.UUID) (int64, error) {
	res, err := st.DB.ExecContext(ctx,
		`UPDATE user_totp SET enabled = true WHERE user_id = $1`, userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// GetTOTPSecret возвращает секрет и включённость проверки. Для пользователя
// без записи — ("", false, nil): второй фактор просто не настроен.
func (st *Store) GetTOTPSecret(ctx context.Context, userID uuid.UUID) (string, bool, error) {
	var secret string
	var enabled bool
	err := st.DB.QueryRowContext(ctx,
		`SELECT secret, enabled FROM user_totp WHERE user_id = $1`, userID).
		Scan(&secret, &enabled)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return secret, enabled, nil
}

// DeleteTOTP полностью убирает второй фактор (после проверки пароля).
func (st *Store) DeleteTOTP(ctx context.Context, userID uuid.UUID) error {
	_, err := st.DB.ExecContext(ctx, `DELETE FROM user_totp WHERE user_id = $1`, userID)
	return err
}
