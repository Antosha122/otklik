-- TOTP (RFC 6238) — второй фактор входа для сотрудников. Секрет хранится
-- у пользователя в приложении-аутентификаторе, сервер его знает, но коды
-- не выдаёт. Проверка — при каждом логине (см. internal/httpapi/auth.go),
-- пока enabled=true. Отключение требует пароль, включение — действующий код.
CREATE TABLE IF NOT EXISTS user_totp (
    user_id    uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    secret     text NOT NULL,             -- base32, 20 байт энтропии
    enabled    boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);
