package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"otklik/internal/domain"
)

// PurgeExpiredSessions СѓРґР°Р»СЏРµС‚ РёСЃС‚С‘РєС€РёРµ СЃРµСЃСЃРёРё РёР· РѕР±РµРёС… С‚Р°Р±Р»РёС†.
// Р’С‹Р·С‹РІР°РµС‚СЃСЏ С‡Р°СЃРѕРІС‹Рј РґР¶Р°РЅРёСЃС‚РѕСЂРѕРј: СЃС‚СЂРѕРєРё СЃ expires_at <= now() СѓР¶Рµ РЅРёРєРµРј
// РЅРµ С‡РёС‚Р°СЋС‚СЃСЏ (РІСЃРµ РїСЂРѕРІРµСЂРєРё СЃРјРѕС‚СЂСЏС‚ expires_at > now()), РЅРѕ РєРѕРїРёС‚СЊ РёС… РЅРµР»СЊР·СЏ вЂ”
// С‚Р°Р±Р»РёС†С‹ СЃРµСЃСЃРёР№ СЂР°СЃС‚СѓС‚ РЅРµРѕРіСЂР°РЅРёС‡РµРЅРЅРѕ. Р’РѕР·РІСЂР°С‰Р°РµС‚ СЃСѓРјРјР°СЂРЅРѕРµ С‡РёСЃР»Рѕ СѓРґР°Р»С‘РЅРЅС‹С… СЃС‚СЂРѕРє.
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

// PurgeTerminalAppeals СѓРґР°Р»СЏРµС‚ С‚РµСЂРјРёРЅР°Р»СЊРЅС‹Рµ РѕР±СЂР°С‰РµРЅРёСЏ (completed / rejected /
// closed_no_response), РЅРµ РѕР±РЅРѕРІР»СЏРІС€РёРµСЃСЏ РґРѕР»СЊС€Рµ retentionDays, РІРјРµСЃС‚Рµ СЃ С‡Р°С‚РѕРј,
// СЃРѕР±С‹С‚РёСЏРјРё, РІР»РѕР¶РµРЅРёСЏРјРё Рё СЃРµСЃСЃРёСЏРјРё Р·Р°СЏРІРёС‚РµР»СЏ (РєР°СЃРєР°Рґ РїРѕ РІРЅРµС€РЅРёРј РєР»СЋС‡Р°Рј).
// Р’РѕР·РІСЂР°С‰Р°РµС‚ РёРґРµРЅС‚РёС„РёРєР°С‚РѕСЂС‹ СѓРґР°Р»С‘РЅРЅС‹С… РѕР±СЂР°С‰РµРЅРёР№ вЂ” РІС‹Р·С‹РІР°СЋС‰Р°СЏ СЃС‚РѕСЂРѕРЅР° РїРѕ РЅРёРј
// СЃС‚РёСЂР°РµС‚ С„Р°Р№Р»С‹ РІР»РѕР¶РµРЅРёР№ СЃ РґРёСЃРєР°. РџРµСЂСЃРѕРЅР°Р»СЊРЅС‹Рµ РґР°РЅРЅС‹Рµ РґРµС‚РµР№ РЅРµ РґРѕР»Р¶РЅС‹
// С…СЂР°РЅРёС‚СЊСЃСЏ Р±РµСЃСЃСЂРѕС‡РЅРѕ: СЃСЂРѕРє Р·Р°РґР°С‘С‚СЃСЏ RETENTION_DAYS, 0 вЂ” С…СЂР°РЅРёС‚СЊ РІРµС‡РЅРѕ.
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

// AutoCloseResult вЂ” РёС‚РѕРі РїСЂРѕРіРѕРЅР°: РёРґРµРЅС‚РёС„РёРєР°С‚РѕСЂС‹ РѕР±СЂР°С‰РµРЅРёР№, РїРѕ РєРѕС‚РѕСЂС‹Рј
// РѕС‚РїСЂР°РІР»РµРЅРѕ РїСЂРµРґСѓРїСЂРµР¶РґРµРЅРёРµ (РґР»СЏ push-СѓРІРµРґРѕРјР»РµРЅРёР№) Рё РєРѕС‚РѕСЂС‹Рµ Р·Р°РєСЂС‹С‚С‹.
type AutoCloseResult struct {
	Warned []uuid.UUID
	Closed []uuid.UUID
}

// AutoCloseNoResponse Р·Р°РєСЂС‹РІР°РµС‚ Р±РµР· РѕС‚РІРµС‚Р° РѕР±СЂР°С‰РµРЅРёСЏ, РіРґРµ Р·Р°СЏРІРёС‚РµР»СЊ РЅРµ РІРѕР·РІСЂР°С‰Р°Р»СЃСЏ
// РґРѕР»СЊС€Рµ no_response_days (РўР— 5.1: В«Р·Р°СЏРІРёС‚РµР»СЊ РЅРµ РІРµСЂРЅСѓР»СЃСЏ N РґРЅРµР№ вЂ” СЃРёСЃС‚РµРјР°В»).
// РђРєС‚РёРІРЅРѕСЃС‚СЊ Р·Р°СЏРІРёС‚РµР»СЏ = РїРѕСЃР»РµРґРЅРµРµ РµРіРѕ СЃРѕРѕР±С‰РµРЅРёРµ РІ С‡Р°С‚Рµ; РµСЃР»Рё СЃРѕРѕР±С‰РµРЅРёР№ РЅРµ Р±С‹Р»Рѕ вЂ”
// РІСЂРµРјСЏ СЃРѕР·РґР°РЅРёСЏ РѕР±СЂР°С‰РµРЅРёСЏ. РЎРЅР°С‡Р°Р»Р° (Р·Р° 2 РґРЅСЏ РґРѕ Р·Р°РєСЂС‹С‚РёСЏ) РІ С‡Р°С‚ РїР°РґР°РµС‚
// РїСЂРµРґСѓРїСЂРµР¶РґРµРЅРёРµ, С‡С‚РѕР±С‹ РІРµСЂРЅСѓРІС€РёР№СЃСЏ СЂРµР±С‘РЅРѕРє РЅРµ РѕР±РЅР°СЂСѓР¶РёР» Р·Р°РєСЂС‹С‚РѕРµ РѕР±СЂР°С‰РµРЅРёРµ
// Р±РµР· РѕР±СЉСЏСЃРЅРµРЅРёР№. Р’РѕР·РІСЂР°С‰Р°РµС‚ РїСЂРµРґСѓРїСЂРµР¶РґС‘РЅРЅС‹Рµ Рё Р·Р°РєСЂС‹С‚С‹Рµ РѕР±СЂР°С‰РµРЅРёСЏ.
func (st *Store) AutoCloseNoResponse(ctx context.Context) (AutoCloseResult, error) {
	var res AutoCloseResult
	set, err := st.GetSettings(ctx)
	if err != nil {
		return res, err
	}
	cutoff := time.Now().AddDate(0, 0, -set.NoResponseDays)

	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()

	// РђРІС‚РѕР·Р°РєСЂС‹С‚РёРµ Р±РѕР»СЊС€Рµ РЅРµ РјРѕР»С‡Р°Р»РёРІРѕРµ: СЃРЅР°С‡Р°Р»Р° РІ С‡Р°С‚ РїР°РґР°РµС‚ РїСЂРµРґСѓРїСЂРµР¶РґРµРЅРёРµ,
	// Рё С‚РѕР»СЊРєРѕ СЃРїСѓСЃС‚СЏ ~2 РґРЅСЏ РїРѕСЃР»Рµ РЅРµРіРѕ РѕР±СЂР°С‰РµРЅРёРµ Р·Р°РєСЂС‹РІР°РµС‚СЃСЏ. Р РµР±С‘РЅРѕРє,
	// РІРµСЂРЅСѓРІС€РёР№СЃСЏ РЅР° 15-Р№ РґРµРЅСЊ, СЂР°РЅСЊС€Рµ РѕР±РЅР°СЂСѓР¶РёРІР°Р» Р±С‹ Р·Р°РєСЂС‹С‚РѕРµ РѕР±СЂР°С‰РµРЅРёРµ Р±РµР·
	// РѕР±СЉСЏСЃРЅРµРЅРёР№. РџСЂРµС„РёРєСЃ С‚РµРєСЃС‚Р° вЂ” РјР°СЂРєРµСЂ РёРґРµРјРїРѕС‚РµРЅС‚РЅРѕСЃС‚Рё (РІС‚РѕСЂРѕР№ СЂР°Р· С‚Рѕ Р¶Рµ
	// СЃРѕРѕР±С‰РµРЅРёРµ РЅРµ РІСЃС‚Р°РІР»СЏРµС‚СЃСЏ), Р° СѓСЃР»РѕРІРёРµ В«РїСЂРµРґСѓРїСЂРµР¶РґРµРЅРёРµ РµСЃС‚СЊВ» РІ Р·Р°РєСЂС‹РІР°СЋС‰РµРј
	// Р·Р°РїСЂРѕСЃРµ РіР°СЂР°РЅС‚РёСЂСѓРµС‚ РїР°СѓР·Сѓ РјРёРЅРёРјСѓРј РІ 2 РґРЅСЏ РјРµР¶РґСѓ РїСЂРµРґСѓРїСЂРµР¶РґРµРЅРёРµРј Рё Р·Р°РєСЂС‹С‚РёРµРј.
	const warnPrefix = "вљ  РђРІС‚РѕР·Р°РєСЂС‹С‚РёРµ"
	const warnText = "вљ  РђРІС‚РѕР·Р°РєСЂС‹С‚РёРµ: РјС‹ РґР°РІРЅРѕ РЅРµ РІРёРґРµР»Рё РІР°С€РµРіРѕ РѕС‚РІРµС‚Р°. " +
		"Р•СЃР»Рё СЃРёС‚СѓР°С†РёСЏ РµС‰С‘ Р°РєС‚СѓР°Р»СЊРЅР° вЂ” РЅР°РїРёС€РёС‚Рµ Р»СЋР±РѕРµ СЃРѕРѕР±С‰РµРЅРёРµ, Рё РѕР±СЂР°С‰РµРЅРёРµ РїСЂРѕРґРѕР»Р¶РёС‚СЃСЏ. " +
		"Р‘РµР· РѕС‚РІРµС‚Р° РѕРЅРѕ Р±СѓРґРµС‚ Р·Р°РєСЂС‹С‚Рѕ С‡РµСЂРµР· 2 РґРЅСЏ."
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
			return res, err
		}
		var warnIDs []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return res, err
			}
			warnIDs = append(warnIDs, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return res, err
		}
		rows.Close()
		for _, id := range warnIDs {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO messages (appeal_id, author_type, author_id, text)
				VALUES ($1, 'operator', NULL, $2)`, id, warnText); err != nil {
				return res, err
			}
		}
		res.Warned = warnIDs
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
		return res, err
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
			return res, err
		}
		ids = append(ids, id)
		statuses = append(statuses, status)
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return res, err
	}
	rows.Close()

	for i, id := range ids {
		upd, err := tx.ExecContext(ctx, `
			UPDATE appeals SET status = 'closed_no_response',
				version = version + 1, updated_at = now()
			WHERE id = $1 AND status = $2 AND version = $3`,
			id, string(statuses[i]), versions[i])
		if err != nil {
			return res, err
		}
		if n, _ := upd.RowsAffected(); n == 0 {
			continue
		}
		if err := addEvent(ctx, tx, EventPayload{
			AppealID: id, ActorRole: domain.Role("system"),
			EventType: "status", OldValue: string(statuses[i]),
			NewValue: string(domain.StatusClosedNoResponse),
			Reason:   "no_applicant_response",
		}); err != nil {
			return res, err
		}
		res.Closed = append(res.Closed, id)
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	return res, nil
}
