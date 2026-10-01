package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type Store struct {
	DB *sql.DB
}

func New(db *sql.DB) *Store { return &Store{DB: db} }

type User struct {
	ID                 uuid.UUID `json:"id"`
	Login              string    `json:"login"`
	Role               string    `json:"role"`
	SpecialistGroup    string    `json:"specialist_group"`
	Active             bool      `json:"active"`
	MustChangePassword bool      `json:"must_change_password"`
	CreatedAt          time.Time `json:"created_at"`
}

const userCols = `id, login, role, specialist_group, active, must_change_password, created_at`

func scanUser(s interface{ Scan(...any) error }) (User, error) {
	var u User
	err := s.Scan(&u.ID, &u.Login, &u.Role, &u.SpecialistGroup, &u.Active, &u.MustChangePassword, &u.CreatedAt)
	return u, err
}

func (st *Store) CreateUser(ctx context.Context, login, passwordHash, role, group string) (User, error) {
	row := st.DB.QueryRowContext(ctx, `
		INSERT INTO users (login, password_hash, role, specialist_group)
		VALUES ($1, $2, $3, $4)
		RETURNING `+userCols, login, passwordHash, role, group)
	return scanUser(row)
}

func (st *Store) GetUserByLogin(ctx context.Context, login string) (User, string, error) {
	row := st.DB.QueryRowContext(ctx,
		`SELECT `+userCols+`, password_hash FROM users WHERE login = $1`, login)
	var u User
	var hash string
	err := row.Scan(&u.ID, &u.Login, &u.Role, &u.SpecialistGroup, &u.Active, &u.MustChangePassword, &u.CreatedAt, &hash)
	return u, hash, err
}

func (st *Store) GetUserByID(ctx context.Context, id uuid.UUID) (User, error) {
	row := st.DB.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id)
	return scanUser(row)
}

func (st *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := st.DB.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY role, login`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (st *Store) UpdateUser(ctx context.Context, id uuid.UUID, role, group *string, active *bool) error {
	_, err := st.DB.ExecContext(ctx, `
		UPDATE users SET
			role = COALESCE($2, role),
			specialist_group = COALESCE($3, specialist_group),
			active = COALESCE($4, active)
		WHERE id = $1`, id, role, group, active)
	return err
}

func (st *Store) SetUserActive(ctx context.Context, id uuid.UUID, active bool) error {
	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET active = $2 WHERE id = $1`, id, active); err != nil {
		return err
	}
	if !active {
		if _, err := tx.ExecContext(ctx, `DELETE FROM staff_sessions WHERE user_id = $1`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpdateUserPassword заменяет хеш пароля пользователя (смена пароля самим
// сотрудником) и снимает флаг обязательной смены демо-пароля.
func (st *Store) UpdateUserPassword(ctx context.Context, id uuid.UUID, passwordHash string) error {
	_, err := st.DB.ExecContext(ctx,
		`UPDATE users SET password_hash = $2, must_change_password = false WHERE id = $1`,
		id, passwordHash)
	return err
}

// SetMustChangePassword выставляет/снимает флаг обязательной смены пароля.
// Используется при логине для сверки флага с фактом: если пароль уже не
// демо-пароль, требовать смену больше нельзя.
func (st *Store) SetMustChangePassword(ctx context.Context, id uuid.UUID, v bool) error {
	_, err := st.DB.ExecContext(ctx,
		`UPDATE users SET must_change_password = $2 WHERE id = $1`,
		id, v)
	return err
}

// DeleteStaffSessionsForUser удаляет все сессии пользователя, кроме exceptTokenHash
// (пустой exceptTokenHash — удалить все, используется при деактивации).
func (st *Store) DeleteStaffSessionsForUser(ctx context.Context, userID uuid.UUID, exceptTokenHash string) error {
	_, err := st.DB.ExecContext(ctx,
		`DELETE FROM staff_sessions WHERE user_id = $1 AND ($2 = '' OR token_hash <> $2)`,
		userID, exceptTokenHash)
	return err
}

func (st *Store) CreateStaffSession(ctx context.Context, tokenHash string, userID uuid.UUID, expires time.Time) error {
	_, err := st.DB.ExecContext(ctx,
		`INSERT INTO staff_sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, userID, expires)
	return err
}

func (st *Store) GetStaffSession(ctx context.Context, tokenHash string) (uuid.UUID, bool, error) {
	var userID uuid.UUID
	err := st.DB.QueryRowContext(ctx,
		`SELECT user_id FROM staff_sessions WHERE token_hash = $1 AND expires_at > now()`,
		tokenHash).Scan(&userID)
	if err == sql.ErrNoRows {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return userID, true, nil
}

func (st *Store) DeleteStaffSession(ctx context.Context, tokenHash string) error {
	_, err := st.DB.ExecContext(ctx, `DELETE FROM staff_sessions WHERE token_hash = $1`, tokenHash)
	return err
}

func (st *Store) CreateApplicantSession(ctx context.Context, tokenHash string, appealID uuid.UUID, expires time.Time) error {
	_, err := st.DB.ExecContext(ctx,
		`INSERT INTO applicant_sessions (token_hash, appeal_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, appealID, expires)
	return err
}

func (st *Store) GetApplicantSession(ctx context.Context, tokenHash string) (uuid.UUID, bool, error) {
	var appealID uuid.UUID
	err := st.DB.QueryRowContext(ctx,
		`SELECT appeal_id FROM applicant_sessions WHERE token_hash = $1 AND expires_at > now()`,
		tokenHash).Scan(&appealID)
	if err == sql.ErrNoRows {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return appealID, true, nil
}

func (st *Store) DeleteApplicantSession(ctx context.Context, tokenHash string) error {
	_, err := st.DB.ExecContext(ctx, `DELETE FROM applicant_sessions WHERE token_hash = $1`, tokenHash)
	return err
}
