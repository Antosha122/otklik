package config_test

import (
	"testing"
	"time"

	"otklik/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	for _, k := range []string{"LISTEN_ADDR", "STAFF_LISTEN_ADDR", "DATABASE_URL",
		"ATTACHMENTS_DIR", "COOKIE_SECURE", "SESSION_TTL_MIN", "SEED_DEFAULT_PWD",
		"LOG_FORMAT", "RETENTION_DAYS"} {
		t.Setenv(k, "")
	}
	cfg := config.Load()
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want :8080", cfg.ListenAddr)
	}
	if cfg.StaffListenAddr != ":8081" {
		t.Errorf("StaffListenAddr = %q, want :8081", cfg.StaffListenAddr)
	}
	if cfg.DatabaseURL != "postgres://otklik:otklik@localhost:5432/otklik?sslmode=disable" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.AttachmentsDir != "./data/attachments" {
		t.Errorf("AttachmentsDir = %q", cfg.AttachmentsDir)
	}
	if cfg.CookieSecure {
		t.Error("CookieSecure should be false by default")
	}
	if cfg.SessionTTL != 2*time.Hour {
		t.Errorf("SessionTTL = %v, want 2h", cfg.SessionTTL)
	}
	if cfg.SeedDefaultPwd != "otklik-demo-2026" {
		t.Errorf("SeedDefaultPwd = %q", cfg.SeedDefaultPwd)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("LISTEN_ADDR", "127.0.0.1:9000")
	t.Setenv("STAFF_LISTEN_ADDR", "127.0.0.1:9001")
	t.Setenv("DATABASE_URL", "postgres://u:p@db:5432/x?sslmode=disable")
	t.Setenv("ATTACHMENTS_DIR", "/tmp/att")
	t.Setenv("SEED_DEFAULT_PWD", "secret-pwd")
	t.Setenv("COOKIE_SECURE", "true")
	t.Setenv("SESSION_TTL_MIN", "45")
	cfg := config.Load()
	if cfg.ListenAddr != "127.0.0.1:9000" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.StaffListenAddr != "127.0.0.1:9001" {
		t.Errorf("StaffListenAddr = %q", cfg.StaffListenAddr)
	}
	if cfg.DatabaseURL != "postgres://u:p@db:5432/x?sslmode=disable" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.AttachmentsDir != "/tmp/att" {
		t.Errorf("AttachmentsDir = %q", cfg.AttachmentsDir)
	}
	if cfg.SeedDefaultPwd != "secret-pwd" {
		t.Errorf("SeedDefaultPwd = %q", cfg.SeedDefaultPwd)
	}
	if !cfg.CookieSecure {
		t.Error("COOKIE_SECURE=true should enable CookieSecure")
	}
	if cfg.SessionTTL != 45*time.Minute {
		t.Errorf("SessionTTL = %v, want 45m", cfg.SessionTTL)
	}
}

func TestLoadCookieSecureValues(t *testing.T) {
	cases := map[string]bool{"1": true, "true": true, "0": false, "yes": false, "": false}
	for v, want := range cases {
		t.Setenv("COOKIE_SECURE", v)
		if got := config.Load().CookieSecure; got != want {
			t.Errorf("COOKIE_SECURE=%q: CookieSecure = %v, want %v", v, got, want)
		}
	}
}

func TestLoadSessionTTLInvalid(t *testing.T) {
	for _, v := range []string{"abc", "0", "-5", ""} {
		t.Setenv("SESSION_TTL_MIN", v)
		if got := config.Load().SessionTTL; got != 2*time.Hour {
			t.Errorf("SESSION_TTL_MIN=%q: SessionTTL = %v, want default 2h", v, got)
		}
	}
}
