package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"otklik/internal/config"
	"otklik/internal/push"
)

// newPushSender строит отправитель Web Push из конфигурации; nil — push
// выключен (ключи VAPID не заданы). Ошибка конфигурации не роняет сервер:
// логируем и работаем без пушей — сайт важнее уведомлений.
func newPushSender(cfg config.Config) *push.Sender {
	s, err := push.NewSender(cfg.VapidPublicKey, cfg.VapidPrivateKey, cfg.PushSubject)
	if err != nil {
		slog.Error("push: некорректные VAPID-ключи, Web Push отключён", "error", err.Error())
		return nil
	}
	return s
}

// handlePushConfig — публичный ключ приложения для pushManager.subscribe.
type pushConfigResp struct {
	Enabled   bool   `json:"enabled"`
	PublicKey string `json:"public_key"`
}

func (s *Server) handlePushConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, pushConfigResp{
		Enabled:   s.push != nil,
		PublicKey: s.cfg.VapidPublicKey,
	})
}

type pushSubscribeReq struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256DH string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// handlePushSubscribe — заявитель подписывается на уведомления по обращению
// (сессия заявителя обязательна: подписка привязывается к обращению).
func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var req pushSubscribeReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if !strings.HasPrefix(req.Endpoint, "https://") ||
		len(req.Keys.P256DH) < 40 || len(req.Keys.Auth) < 16 {
		writeJSON(w, http.StatusBadRequest, errorResp{"invalid push subscription"})
		return
	}
	if err := s.st.SavePushSubscription(r.Context(), p.AppealID, req.Endpoint, req.Keys.P256DH, req.Keys.Auth); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "subscribed"})
}

// handlePushUnsubscribe — отписка; чужую подписку снять нельзя (appeal_id).
func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var req pushSubscribeReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Endpoint == "" {
		writeJSON(w, http.StatusBadRequest, errorResp{"endpoint is required"})
		return
	}
	if err := s.st.DeletePushSubscription(r.Context(), p.AppealID, req.Endpoint); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unsubscribed"})
}

// notifyApplicant рассылает push всем подпискам обращения. Вызывается в
// горутине после записи события: доставка не должна задерживать ответ API.
// Отработавшие подписки (404/410) удаляются из базы.
func (s *Server) notifyApplicant(ctx context.Context, appealID uuid.UUID, title, body string) {
	if s.push == nil || s.st == nil {
		return
	}
	subs, err := s.st.PushSubscriptionsForAppeal(ctx, appealID)
	if err != nil {
		slog.Error("push: список подписок", "appeal_id", appealID, "error", err.Error())
		return
	}
	if len(subs) == 0 {
		return
	}
	ps := make([]push.Subscription, 0, len(subs))
	for _, sub := range subs {
		ps = append(ps, push.Subscription{Endpoint: sub.Endpoint, P256DH: sub.P256DH, Auth: sub.Auth})
	}
	dead := s.push.Notify(ctx, ps, push.Message{Title: title, Body: body, URL: "/appeal"})
	for _, ep := range dead {
		if err := s.st.DeletePushSubscription(ctx, appealID, ep); err != nil {
			slog.Warn("push: удаление мёртвой подписки", "error", err.Error())
		}
	}
	if len(dead) > 0 {
		slog.Info("push: удалены отозванные подписки", "appeal_id", appealID, "count", len(dead))
	}
}

// notifyApplicantAsync — обёртка для вызова из обработчиков: отдельная
// горутина и собственный таймаут-контекст, не привязанный к запросу.
func (s *Server) notifyApplicantAsync(appealID uuid.UUID, title, body string) {
	if s.push == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.notifyApplicant(ctx, appealID, title, body)
	}()
}
