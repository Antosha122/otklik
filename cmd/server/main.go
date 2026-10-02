package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"otklik/internal/config"
	"otklik/internal/db"
	"otklik/internal/httpapi"
	"otklik/internal/push"
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

	// Подстраховка на прод: эти два флага легко забыть выставить в .env.
	if !cfg.CookieSecure {
		slog.Warn("config: COOKIE_SECURE=0 — куки сессий уходят и по HTTP и могут быть перехвачены; на проде за Caddy обязательно COOKIE_SECURE=1")
	}
	if cfg.TrackHmacKey == "" {
		slog.Warn("config: TRACK_HMAC_KEY не задан — хеши трек-номеров несолёные (SHA-256); задайте длинную случайную строку (обращения, созданные до ключа, останутся доступными)")
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
	// SEED_DEFAULT_PWD не задан: пароль сгенерирован случайно и никому не известен.
	// Печатаем его один раз (только в этот лог) — войти и сменить его нужно сразу.
	if cfg.SeedPwdGenerated {
		slog.Info("SEED_DEFAULT_PWD is empty: random demo password generated (shown once, must be changed on first login)",
			"password", cfg.SeedDefaultPwd)
	}

	st := store.New(d)

	// Словарь кризисных маркеров: при первом старте наполняется встроенными
	// значениями, дальше его редактирует администратор (панель /admin).
	if err := st.EnsureCrisisMarkersSeeded(ctx); err != nil {
		fatal("seed crisis markers", "error", err)
	}

	// Web Push: отправитель для джанистора (httpapi создаёт свой из того же
	// конфига). nil — ключи VAPID не заданы, пуши выключены.
	pushSender, err := push.NewSender(cfg.VapidPublicKey, cfg.VapidPrivateKey, cfg.PushSubject)
	if err != nil {
		fatal("push: некорректные VAPID-ключи", "error", err)
	}
	notifyAppeal := func(appealID uuid.UUID, title, body string) {
		if pushSender == nil {
			return
		}
		subs, err := st.PushSubscriptionsForAppeal(ctx, appealID)
		if err != nil || len(subs) == 0 {
			return
		}
		list := make([]push.Subscription, 0, len(subs))
		for _, sub := range subs {
			list = append(list, push.Subscription{Endpoint: sub.Endpoint, P256DH: sub.P256DH, Auth: sub.Auth})
		}
		for _, ep := range pushSender.Notify(ctx, list, push.Message{Title: title, Body: body, URL: "/appeal"}) {
			_ = st.DeletePushSubscription(ctx, appealID, ep)
		}
	}

	// ТЗ 5.1: «закрыто без ответа» переводит система — фоновый джоб
	// закрывает обращения, где заявитель не возвращался дольше N дней.
	// Он же раз в час подчищает истёкшие сессии (обе таблицы).
	// Shutdown: джоб живёт в горутине, main обязан дождаться её завершения
	// (WaitGroup) — иначе PurgeTerminalAppeals оборвётся посреди удаления
	// файлов вложений и останутся осиротевшие каталоги, которые никто
	// больше не удалит. БД-фазы получают отменяемый ctx, а файловая фаза
	// ретеншна — WithoutCancel: начатую чистку диска важно докончить.
	var janitorWG sync.WaitGroup
	janitorWG.Add(1)
	go func() {
		defer janitorWG.Done()
		run := func() {
			res, err := st.AutoCloseNoResponse(ctx)
			if err != nil {
				if ctx.Err() == nil {
					slog.Error("janitor: auto-close", "error", err)
				}
			} else {
				if len(res.Closed) > 0 {
					slog.Info("janitor: closed appeals without applicant response", "count", len(res.Closed))
				}
				// Предупреждение об автозакрытии — самое важное push-уведомление:
				// у заявителя ещё 2 дня, чтобы вернуться и спасти обращение.
				for _, id := range res.Warned {
					notifyAppeal(id, "Отклик — обращение скоро закроется",
						"Мы давно не видели вашего ответа. Напишите сообщение, чтобы обращение продолжилось.")
				}
			}
			if ctx.Err() != nil {
				return
			}
			purged, err := st.PurgeExpiredSessions(ctx)
			if err != nil {
				if ctx.Err() == nil {
					slog.Error("janitor: purge sessions", "error", err)
				}
			} else if purged > 0 {
				slog.Info("janitor: purged expired sessions", "count", purged)
			}
			// Retention: терминальные обращения старше RETENTION_DAYS удаляются
			// вместе с файлами вложений — персональные данные не хранятся вечно.
			if cfg.RetentionDays > 0 {
				ids, err := st.PurgeTerminalAppeals(context.WithoutCancel(ctx), cfg.RetentionDays)
				if err != nil {
					slog.Error("janitor: retention purge", "error", err)
				} else if len(ids) > 0 {
					// Раз удаления из БД прошли, каталоги вложений обязаны
					// исчезнуть до конца — отсюда и WithoutCancel выше.
					dirs := 0
					for _, id := range ids {
						if err := os.RemoveAll(filepath.Join(cfg.AttachmentsDir, id.String())); err == nil {
							dirs++
						} else {
							slog.Error("janitor: remove attachment dir", "appeal_id", id, "error", err)
						}
					}
					slog.Info("janitor: purged terminal appeals",
						"count", len(ids), "attachment_dirs_removed", dirs, "retention_days", cfg.RetentionDays)
				}
			}
		}
		run()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				slog.Info("janitor: stopping")
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
	// Джанистор мог быть в середине чистки: ждём его завершения, но не
	// дольше собственного бюджета (ретеншн-фаза неотменяема сознательно —
	// доканчивает начатое удаление, обычно это секунды).
	janitorDone := make(chan struct{})
	go func() {
		janitorWG.Wait()
		close(janitorDone)
	}()
	select {
	case <-janitorDone:
		slog.Info("otklik: stopped")
	case <-time.After(15 * time.Second):
		slog.Error("janitor: graceful stop timed out, exiting anyway")
	}
}
