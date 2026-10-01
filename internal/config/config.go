package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strconv"
	"time"
)

type Config struct {
	ListenAddr      string
	StaffListenAddr string
	DatabaseURL     string
	AttachmentsDir  string
	CookieSecure    bool
	SessionTTL      time.Duration
	SeedDefaultPwd  string
	// SeedPwdGenerated: SEED_DEFAULT_PWD не задан, пароль сгенерирован случайно —
	// main печатает его в лог один раз (иначе в демо-аккаунты никто не войдёт).
	SeedPwdGenerated bool
	LogFormat        string // json (по умолчанию, для прода) или text (локальная разработка)
	RetentionDays    int    // срок хранения терминальных обращений; 0 — хранить бессрочно
	// TotpEnabled: 2FA (TOTP) для сотрудников. По умолчанию выключена — на демо-стенде
	// она мешает (код нужен при каждом входе); в проде включается TOTP_ENABLED=1.
	TotpEnabled bool
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func Load() Config {
	secure := false
	if v := os.Getenv("COOKIE_SECURE"); v == "1" || v == "true" {
		secure = true
	}
	ttl := 2 * time.Hour
	if v := os.Getenv("SESSION_TTL_MIN"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ttl = time.Duration(n) * time.Minute
		}
	}
	retention := 0
	if v := os.Getenv("RETENTION_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			retention = n
		}
	}
	// Дефолтного демо-пароля в коде быть не должно: он был бы известен каждому,
	// кто видел репозиторий. Если SEED_DEFAULT_PWD не задан — генерируем
	// случайный (main напечатает его в лог один раз, при первом старте).
	seedPwd, seedPwdGenerated := os.Getenv("SEED_DEFAULT_PWD"), false
	if seedPwd == "" {
		buf := make([]byte, 12)
		if _, err := rand.Read(buf); err != nil {
			panic("config: crypto/rand unavailable for seed password: " + err.Error())
		}
		seedPwd, seedPwdGenerated = "seed-"+hex.EncodeToString(buf), true
	}
	totpEnabled := false
	if v := os.Getenv("TOTP_ENABLED"); v == "1" || v == "true" {
		totpEnabled = true
	}
	return Config{
		ListenAddr:       env("LISTEN_ADDR", ":8080"),
		StaffListenAddr:  env("STAFF_LISTEN_ADDR", ":8081"),
		DatabaseURL:      env("DATABASE_URL", "postgres://otklik:otklik@localhost:5432/otklik?sslmode=disable"),
		AttachmentsDir:   env("ATTACHMENTS_DIR", "./data/attachments"),
		CookieSecure:     secure,
		SessionTTL:       ttl,
		SeedDefaultPwd:   seedPwd,
		SeedPwdGenerated: seedPwdGenerated,
		LogFormat:        env("LOG_FORMAT", "json"),
		RetentionDays:    retention,
		TotpEnabled:      totpEnabled,
	}
}
