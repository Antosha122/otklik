package store

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"otklik/internal/domain"
)

// CrisisMarker — редактируемый администратором кризисный маркер.
// kind = substring — проверка как подстрока (ловит словоформы),
// kind = word — проверка по границе слова (короткие слова без ложных
// срабатываний, см. domain.DetectCrisisWith).
type CrisisMarker struct {
	ID   uuid.UUID `json:"id"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
}

// EnsureCrisisMarkersSeeded наполняет таблицу встроенными маркерами при первом
// старте — после этого единственный источник правды БД, администратор может
// пополнять и удалять словарь без пересборки.
func (st *Store) EnsureCrisisMarkersSeeded(ctx context.Context) error {
	var n int
	if err := st.DB.QueryRowContext(ctx, `SELECT count(*) FROM crisis_markers`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, m := range domain.CrisisMarkers {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO crisis_markers (kind, text) VALUES ('substring', $1)`, m); err != nil {
			return err
		}
	}
	for _, m := range domain.CrisisWordMarkers {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO crisis_markers (kind, text) VALUES ('word', $1)`, m); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (st *Store) ListCrisisMarkers(ctx context.Context) ([]CrisisMarker, error) {
	rows, err := st.DB.QueryContext(ctx,
		`SELECT id, kind, text FROM crisis_markers ORDER BY kind, text`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CrisisMarker
	for rows.Next() {
		var m CrisisMarker
		if err := rows.Scan(&m.ID, &m.Kind, &m.Text); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (st *Store) AddCrisisMarker(ctx context.Context, kind, text string) (CrisisMarker, error) {
	m := CrisisMarker{Kind: kind, Text: text}
	err := st.DB.QueryRowContext(ctx, `
		INSERT INTO crisis_markers (kind, text) VALUES ($1, $2)
		ON CONFLICT (kind, text) DO UPDATE SET text = EXCLUDED.text
		RETURNING id`, kind, text).Scan(&m.ID)
	return m, err
}

func (st *Store) DeleteCrisisMarker(ctx context.Context, id uuid.UUID) error {
	res, err := st.DB.ExecContext(ctx, `DELETE FROM crisis_markers WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("crisis marker not found")
	}
	return nil
}

// CrisisLists возвращает словарь для детектора: подстроки и слова.
func (st *Store) CrisisLists(ctx context.Context) (substrings, words []string, err error) {
	markers, err := st.ListCrisisMarkers(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, m := range markers {
		m.Text = strings.TrimSpace(m.Text)
		if m.Text == "" {
			continue
		}
		if m.Kind == "word" {
			words = append(words, m.Text)
		} else {
			substrings = append(substrings, m.Text)
		}
	}
	return substrings, words, nil
}
