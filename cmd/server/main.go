package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"otklik/internal/config"
	"otklik/internal/db"
	"otklik/internal/httpapi"
	"otklik/internal/store"
)

func main() {
	cfg := config.Load()

	// Структурные логи: в проде — JSON (собирается Docker'ом и парсится
	// лог-коллектором), при LOG_FORMAT=text — читаемый человеко-понятный вид.
	var handler slog.Handler = slog.NewJSONHandler(os.Stdout, nil)
	if cfg.LogFormat == "text" {
		handler = slog.NewTextHandler(os.Stdout, nil)
	}
	slog.SetDefault(slog.New(handler))
	fatal := func(msg string, args ...any) {
		slog.Error(msg, args...)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(cfg.AttachmentsDir, 0o755); err != nil {
		fatal("attachments dir", "error", err)
	}

	d, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fatal("db open", "error", err)
	}
	defer d.Close()

	if err := db.Migrate(ctx, d); err != nil {
		fatal("migrate", "error", err)
	}
	if err := db.Seed(ctx, d, cfg.SeedDefaultPwd); err != nil {
		fatal("seed", "error", err)
	}

	st := store.New(d)

	// ТЗ 5.1: «закрыто без ответа» переводит система — фоновый джоб
	// закрывает обращения, где заявитель не возвращался дольше N дней.
	// Он же раз в час подчищает истёкшие сессии (обе таблицы).
	go func() {
		run := func() {
			n, err := st.AutoCloseNoResponse(ctx)
			if err != nil {
				slog.Error("janitor: auto-close", "error", err)
			} else if n > 0 {
				slog.Info("janitor: closed appeals without applicant response", "count", n)
			}
			purged, err := st.PurgeExpiredSessions(ctx)
			if err != nil {
				slog.Error("janitor: purge sessions", "error", err)
			} else if purged > 0 {
				slog.Info("janitor: purged expired sessions", "count", purged)
			}
		}
		run()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()

	publicSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           httpapi.New(cfg, st),
		ReadHeaderTimeout: 10 * time.Second,
	}
	staffSrv := &http.Server{
		Addr:              cfg.StaffListenAddr,
		Handler:           httpapi.NewStaff(cfg, st),
		ReadHeaderTimeout: 10 * time.Second,
	}

	run := func(name string, srv *http.Server) {
		go func() {
			slog.Info("otklik: listening", "port", name, "addr", srv.Addr)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				fatal("listen", "error", err)
			}
		}()
	}
	run("public (заявители)", publicSrv)
	run("staff (сотрудники)", staffSrv)

	<-ctx.Done()
	slog.Info("otklik: shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := publicSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown public", "error", err)
	}
	if err := staffSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown staff", "error", err)
	}
}
