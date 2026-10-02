package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"otklik/internal/domain"
)

// PurgeExpiredSessions удаляет истёкшие сессии из обеих таблиц.
// Вызывается часовым джанистором: строки с expires_at <= now() уже никем
// не читаются (все проверки смотрят expires_at > now()), но копить их нельзя —
// таблицы сессий растут неограниченно. Возвращает суммарное число удалённых строк.
func (st *Store) PurgeExpiredSessions(ctx context.Context) (int64, error) {
	var total int64
	for _, table := range []string{"staff_sessions", "applicant_sessions"} {
		res, err := st.DB.ExecContext(ctx,
			`DELETE FROM `+table+` WHERE expires_at <= now()`)
		if err != nil {
			return total, err
		}
		if n, err := res.RowsAffected(); err == nil {
			total += n
		}
	}
	return total, nil
}

// PurgeTerminalAppeals удаляет терминальные обращения (completed / rejected /
// closed_no_response), не обновлявшиеся дольше retentionDays, вместе с чатом,
// событиями, вложениями и сессиями заявителя (каскад по внешним ключам).
// Возвращает идентификаторы удалённых обращений — вызывающая сторона по ним
// стирает файлы вложений с диска. Персональные данные детей не должны
// храниться бессрочно: срок задаётся RETENTION_DAYS, 0 — хранить вечно.
func (st *Store) PurgeTerminalAppeals(ctx context.Context, retentionDays int) ([]uuid.UUID, error) {
	if retentionDays <= 0 {
		return nil, nil
	}
	rows, err := st.DB.QueryContext(ctx, `
		SELECT id FROM appeals
		WHERE status IN ('completed','rejected','closed_no_response')
		  AND updated_at < now() - make_interval(days => $1)
		ORDER BY updated_at`, retentionDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return ids, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return ids, err
	}
	for _, id := range ids {
		if _, err := st.DB.ExecContext(ctx,
			`DELETE FROM appeals WHERE id = $1 AND status IN ('completed','rejected','closed_no_response')`,
			id); err != nil {
			return ids, err
		}
	}
	return ids, nil
}

// AutoCloseNoResponse закрывает без ответа обращения, где заявитель не возвращался
// дольше no_response_days (ТЗ 5.1: «заявитель не вернулся N дней — система»).
// Активность заявителя = последнее его сообщение в чате; если сообщений не было —
// время создания обращения. Возвращает число закрытых обращений.
func (st *Store) AutoCloseNoResponse(ctx context.Context) (int, error) {
	set, err := st.GetSettings(ctx)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().AddDate(0, 0, -set.NoResponseDays)

	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Автозакрытие больше не молчаливое: сначала в чат падает предупреждение,
	// и только спустя ~2 дня после него обращение закрывается. Ребёнок,
	// вернувшийся на 15-й день, раньше обнаруживал бы закрытое обращение без
	// объяснений. Префикс текста — маркер идемпотентности (второй раз то же
	// сообщение не вставляется), а условие «предупреждение есть» в закрывающем
	// запросе гарантирует паузу минимум в 2 дня между предупреждением и закрытием.
	const warnPrefix = "⚠ Автозакрытие"
	const warnText = "⚠ Автозакрытие: мы давно не видели вашего ответа. " +
		"Если ситуация ещё актуальна — напишите любое сообщение, и обращение продолжится. " +
		"Без ответа оно будет закрыто через 2 дня."
	if set.NoResponseDays >= 3 {
		warnCutoff := time.Now().AddDate(0, 0, -(set.NoResponseDays - 2))
		rows, err := tx.QueryContext(ctx, `
			SELECT a.id
			FROM appeals a
			WHERE a.status IN ('needs_clarification', 'answer_ready')
			  AND COALESCE((SELECT max(m.created_at) FROM messages m
			                WHERE m.appeal_id = a.id AND m.author_type = 'applicant'),
			               a.created_at) < $1
			  AND NOT EXISTS (SELECT 1 FROM messages w
			                WHERE w.appeal_id = a.id AND w.author_type = 'operator'
			                  AND w.text LIKE $2)
			FOR UPDATE OF a`, warnCutoff, warnPrefix+"%")
		if err != nil {
			return 0, err
		}
		var warnIDs []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return 0, err
			}
			warnIDs = append(warnIDs, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return 0, err
		}
		rows.Close()
		for _, id := range warnIDs {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO messages (appeal_id, author_type, author_id, text)
				VALUES ($1, 'operator', NULL, $2)`, id, warnText); err != nil {
				return 0, err
			}
		}
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT a.id, a.status, a.version
		FROM appeals a
		WHERE a.status IN ('needs_clarification', 'answer_ready')
		  AND COALESCE((SELECT max(m.created_at) FROM messages m
		                WHERE m.appeal_id = a.id AND m.author_type = 'applicant'),
		               a.created_at) < $1
		  AND EXISTS (SELECT 1 FROM messages w
		                WHERE w.appeal_id = a.id AND w.author_type = 'operator'
		                  AND w.text LIKE $2)
		ORDER BY a.created_at
		FOR UPDATE OF a`, cutoff, warnPrefix+"%")
	if err != nil {
		return 0, err
	}
	var ids []uuid.UUID
	var statuses []domain.Status
	var versions []int
	for rows.Next() {
		var id uuid.UUID
		var status domain.Status
		var version int
		if err := rows.Scan(&id, &status, &version); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
		statuses = append(statuses, status)
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	closed := 0
	for i, id := range ids {
		res, err := tx.ExecContext(ctx, `
			UPDATE appeals SET status = 'closed_no_response',
				version = version + 1, updated_at = now()
			WHERE id = $1 AND status = $2 AND version = $3`,
			id, string(statuses[i]), versions[i])
		if err != nil {
			return closed, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		if err := addEvent(ctx, tx, EventPayload{
			AppealID: id, ActorRole: domain.Role("system"),
			EventType: "status", OldValue: string(statuses[i]),
			NewValue: string(domain.StatusClosedNoResponse),
			Reason:   "no_applicant_response",
		}); err != nil {
			return closed, err
		}
		closed++
	}
	if err := tx.Commit(); err != nil {
		return closed, err
	}
	return closed, nil
}
