package main

import (
	"context"
	"errors"
	"log"
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(cfg.AttachmentsDir, 0o755); err != nil {
		log.Fatalf("attachments dir: %v", err)
	}

	d, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer d.Close()

	if err := db.Migrate(ctx, d); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	if err := db.Seed(ctx, d, cfg.SeedDefaultPwd); err != nil {
		log.Fatalf("seed: %v", err)
	}

	st := store.New(d)

	// ТЗ 5.1: «закрыто без ответа» переводит система — фоновый джоб
	// закрывает обращения, где заявитель не возвращался дольше N дней.
	// Он же раз в час подчищает истёкшие сессии (обе таблицы).
	go func() {
		run := func() {
			n, err := st.AutoCloseNoResponse(ctx)
			if err != nil {
				log.Printf("janitor: auto-close: %v", err)
			} else if n > 0 {
				log.Printf("janitor: closed %d appeal(s) without applicant response", n)
			}
			purged, err := st.PurgeExpiredSessions(ctx)
			if err != nil {
				log.Printf("janitor: purge sessions: %v", err)
			} else if purged > 0 {
				log.Printf("janitor: purged %d expired session(s)", purged)
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
			log.Printf("otklik: %s listening on %s", name, srv.Addr)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("listen: %v", err)
			}
		}()
	}
	run("public (заявители)", publicSrv)
	run("staff (сотрудники)", staffSrv)

	<-ctx.Done()
	log.Println("otklik: shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := publicSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	if err := staffSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
