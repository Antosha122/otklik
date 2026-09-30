package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func Open(ctx context.Context, databaseURL string) (*sql.DB, error) {
	d, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	d.SetMaxOpenConns(10)
	d.SetMaxIdleConns(5)
	d.SetConnMaxLifetime(time.Hour)
	if err := d.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return d, nil
}

func Migrate(ctx context.Context, d *sql.DB) error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		raw, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return err
		}
		for _, stmt := range splitStatements(string(raw)) {
			if _, err := d.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("migrate %s: %w", e.Name(), err)
			}
		}
		log.Printf("migrations: applied %s", e.Name())
	}
	return nil
}

// Наивное разбиение допустимо: миграции не содержат «;» внутри функций.
func splitStatements(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ";") {
		p := strings.TrimSpace(part)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func Seed(ctx context.Context, d *sql.DB, defaultPwd string) error {
	var catCount, userCount int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM categories`).Scan(&catCount); err != nil {
		return err
	}
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&userCount); err != nil {
		return err
	}

	if catCount == 0 {
		cats := []struct {
			name  string
			group string
			free  bool
		}{
			{"Травля и оскорбления", "psychologists", false},
			{"Конфликт с одноклассниками", "conflictologists", false},
			{"Кибербуллинг", "psychologists", false},
			{"Давление и угрозы", "psychologists", false},
			{"Конфликт с учителем", "conflictologists", false},
			{"Конфликт с родителями", "psychologists", false},
			{"Юридический вопрос", "lawyers", false},
			{"Трудная жизненная ситуация", "social_pedagogues", false},
			{"Не знаю, как это назвать", "psychologists", true},
		}
		for _, c := range cats {
			if _, err := d.ExecContext(ctx,
				`INSERT INTO categories (name, specialist_group, free_form) VALUES ($1, $2, $3)`,
				c.name, c.group, c.free); err != nil {
				return err
			}
		}
		log.Println("seed: categories created")
	}

	if userCount == 0 {
		hash, err := bcrypt.GenerateFromPassword([]byte(defaultPwd), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		users := []struct {
			login string
			role  string
			group string
		}{
			{"admin", "admin", ""},
			{"operator", "operator", ""},
			{"psychologist1", "expert", "psychologists"},
			{"psychologist2", "expert", "psychologists"},
			{"lawyer1", "expert", "lawyers"},
			{"conflictolog1", "expert", "conflictologists"},
			{"social1", "expert", "social_pedagogues"},
		}
		for _, u := range users {
			// must_change_password=true: демо-пароль обязан быть сменён при первом входе.
			if _, err := d.ExecContext(ctx,
				`INSERT INTO users (login, password_hash, role, specialist_group, must_change_password)
				 VALUES ($1, $2, $3, $4, true)`,
				u.login, string(hash), u.role, u.group); err != nil {
				return err
			}
		}
		log.Println("seed: users created (password from SEED_DEFAULT_PWD)")
	}
	return nil
}
