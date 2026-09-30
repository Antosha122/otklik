-- Демо-учётки создаются с паролем из SEED_DEFAULT_PWD. Пока пароль не сменён,
-- любой API-доступ сотрудника закрыт (403 "password change required"),
-- кроме POST /api/auth/password и логаута.
ALTER TABLE users ADD COLUMN IF NOT EXISTS must_change_password boolean NOT NULL DEFAULT false;

-- Backfill: помечаем уже существующие демо-аккаунты (включая старый логин mediator1).
UPDATE users SET must_change_password = true
WHERE login IN ('admin', 'operator', 'psychologist1', 'psychologist2',
                'lawyer1', 'conflictolog1', 'mediator1', 'social1')