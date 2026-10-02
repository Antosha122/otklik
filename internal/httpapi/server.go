package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"otklik/internal/buildinfo"
	"otklik/internal/config"
	"otklik/internal/domain"
	"otklik/internal/store"
)

const (
	staffCookie     = "otklik_staff"
	applicantCookie = "otklik_applicant"
	maxBodySize     = 1 << 20
)

type Server struct {
	cfg      config.Config
	st       *store.Store
	rl       *rateLimiter
	presence *presenceHub
}

// securityHeaders добавляет защитные заголовки ко всем ответам обоих портов.
// HSTS выставляется только при COOKIE_SECURE=true: приложение рассчитано на
// TLS-терминацию перед сервером, а по plain HTTP заголовок игнорируется браузерами.
func securityHeaders(cfg config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Content-Security-Policy",
				"default-src 'self'; script-src 'self'; "+
					"style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; "+
					"connect-src 'self'; object-src 'none'; base-uri 'self'; "+
					"form-action 'self'; frame-ancestors 'none'")
			if cfg.CookieSecure {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// sameOrigin — защита от CSRF для cookie-аутентификации. Браузер на
// cross-site запросе обязан прислать Origin (или Referer): сверяем его хост
// с хостом запроса. Запросы без Origin (curl, мониторинг, тесты) проходят —
// HttpOnly-кука всё равно не покидает сайт, а bearer-токенов больше нет.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			if ref := r.Header.Get("Referer"); ref != "" {
				if u, err := url.Parse(ref); err == nil {
					origin = u.Scheme + "://" + u.Host
				}
			}
		}
		if origin == "" {
			next.ServeHTTP(w, r) // небраузерный клиент
			return
		}
		u, err := url.Parse(origin)
		if err != nil || !sameHost(u.Host, r.Host) {
			writeJSON(w, http.StatusForbidden, errorResp{"cross-origin request rejected"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameHost сравнивает хосты, не обращая внимания на порты по умолчанию
// (example.com и example.com:443 за TLS-прокси — один и тот же источник).
func sameHost(a, b string) bool {
	norm := func(h string) string {
		h = strings.ToLower(strings.TrimSpace(h))
		if host, port, err := net.SplitHostPort(h); err == nil && port != "" {
			if port == "80" || port == "443" {
				return host
			}
			return host + ":" + port
		}
		return h
	}
	return norm(a) == norm(b)
}

// statusWriter запоминает код ответа и размер для access-лога.
// Flush проксируется: SSE-поток присутствия требует явного сброса буфера.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (sw *statusWriter) WriteHeader(code int) {
	if sw.status == 0 {
		sw.status = code
	}
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if sw.status == 0 {
		sw.status = http.StatusOK
	}
	n, err := sw.ResponseWriter.Write(b)
	sw.bytes += n
	return n, err
}

func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// accessLog — единая точка наблюдаемости: request_id сквозной (заголовок
// X-Request-ID возвращается клиенту и попадает в лог), плюс метод, путь,
// код, размер и длительность. 5xx дополнительно видны в error-логе writeErr
// с тем же request_id.
func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqID := middleware.GetReqID(r.Context())
		sw := &statusWriter{ResponseWriter: w}
		if reqID != "" {
			sw.Header().Set("X-Request-ID", reqID)
		}
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		slog.Info("http_request",
			"request_id", reqID,
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"bytes", sw.bytes,
			"duration_ms", float64(time.Since(start).Microseconds())/1000.0,
			"remote", clientIP(r),
		)
	})
}

func baseRouter(cfg config.Config) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(accessLog)
	// RealIP сознательно не включаем: он слепо верит X-Forwarded-For, а тот
	// подделывается клиентом. Адрес для рейт-лимитов считает clientIP —
	// с доверием только приватным прокси (Caddy в docker-сети).
	r.Use(metricsMiddleware)
	r.Use(middleware.Recoverer)
	r.Use(sameOrigin)
	r.Use(securityHeaders(cfg))
	return r
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	// version — из buildinfo (-ldflags при сборке): удобно проверять выкат.
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": buildinfo.Version})
}

func New(cfg config.Config, st *store.Store) http.Handler {
	s := &Server{cfg: cfg, st: st, rl: newRateLimiter(), presence: newPresenceHub()}

	r := baseRouter(cfg)
	r.Use(s.authMW(false, true))

	r.Get("/api/health", healthHandler)
	// Страницы заявителя: каждая логика — отдельный эндпоинт.
	r.Get("/", s.pageFor("applicant", "index.html"))        // приветственное окно
	r.Get("/new", s.pageFor("applicant", "new.html"))       // подача обращения
	r.Get("/track", s.pageFor("applicant", "track.html"))   // вход по трек-номеру
	r.Get("/appeal", s.pageFor("applicant", "appeal.html")) // чат и статус обращения
	r.Get("/sw.js", swHandler())                            // PWA заявителя: service worker с корня (scope "/")
	r.Handle("/assets/*", assetsHandler())
	r.Get("/api/categories", s.handlePublicCategories)
	r.Get("/api/intake-questions", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string][]domain.IntakeQuestion{"questions": domain.IntakeQuestions})
	})
	r.Get("/api/help/crisis", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string][]domain.CrisisHelp{"contacts": domain.CrisisHelpContacts})
	})

	r.Post("/api/appeals", s.handleCreateAppeal)
	r.Post("/api/appeals/track", s.handleVerifyTrack)
	r.Get("/api/me", s.handleMe)

	r.Group(func(r chi.Router) {
		r.Use(s.requireApplicant)
		r.Route("/api/appeals/me", func(r chi.Router) {
			r.Get("/", s.handleApplicantView)
			r.Get("/messages", s.handleApplicantMessages)
			r.Post("/messages", s.handleApplicantPostMessage)
			r.Post("/append", s.handleApplicantAppend)
			r.Post("/result", s.handleApplicantResult)
			r.Post("/feedback", s.handleApplicantFeedback)
			r.Post("/complaint", s.handleApplicantComplaint)
			r.Post("/attachments", s.handleUploadAttachment)
			r.Get("/attachments/{attachmentID}", s.handleDownloadAttachment)
			r.Post("/session/end", s.handleApplicantLogout)
		})
	})

	return r
}

func NewStaff(cfg config.Config, st *store.Store) http.Handler {
	s := &Server{cfg: cfg, st: st, rl: newRateLimiter(), presence: newPresenceHub()}

	r := baseRouter(cfg)
	r.Use(s.authMW(true, false))

	r.Get("/api/health", healthHandler)
	// Метрики (Prometheus) — только на служебном порту: счётчики HTTP-запросов,
	// версия сборки, аптайм. Публичный порт их не отдаёт.
	r.Get("/metrics", metricsHandler)
	// Страницы сотрудника: вход, отдельная панель под каждую роль и карточка обращения.
	r.Get("/", s.pageFor("staff", "login.html")) // редирект на нужную панель делает JS
	r.Get("/login", s.pageFor("staff", "login.html"))
	r.Get("/operator", s.pageFor("staff", "operator.html"))
	r.Get("/expert", s.pageFor("staff", "expert.html"))
	r.Get("/admin", s.pageFor("staff", "admin.html"))
	r.Get("/detail", s.pageFor("staff", "detail.html"))
	r.Get("/detail/*", s.pageFor("staff", "detail.html"))
	r.Get("/profile", s.pageFor("staff", "profile.html"))
	r.Handle("/assets/*", assetsHandler())
	r.Get("/api/categories", s.handlePublicCategories)

	r.Post("/api/auth/login", s.handleLogin)
	r.Post("/api/auth/logout", s.handleLogout)
	r.With(s.requireStaff).Post("/api/auth/password", s.handleChangePassword)
	// Второй фактор (TOTP): настройка и управление — из личного кабинета.
	r.With(s.requireStaff).Get("/api/auth/totp", s.handleTOTPStatus)
	r.With(s.requireStaff).Post("/api/auth/totp/setup", s.handleTOTPSetup)
	r.With(s.requireStaff).Post("/api/auth/totp/enable", s.handleTOTPEnable)
	r.With(s.requireStaff).Post("/api/auth/totp/disable", s.handleTOTPDisable)
	r.Get("/api/me", s.handleMe)

	// Общая группа вне ролевых: в chi поздняя регистрация перекрывает раннюю.
	r.Group(func(r chi.Router) {
		r.Use(s.requireRole(domain.RoleExpert, domain.RoleOperator, domain.RoleAdmin))
		r.Get("/api/staff/experts", s.handleListExperts)
		r.Get("/api/export/appeals", s.handleExportAppeals)
		r.Get("/api/profile", s.handleProfile)
	})

	r.Group(func(r chi.Router) {
		r.Use(s.requireRole(domain.RoleOperator, domain.RoleExpert))
		r.Get("/api/mystats", s.handleMyStats)
	})

	r.Group(func(r chi.Router) {
		r.Use(s.requireRole(domain.RoleOperator, domain.RoleAdmin))
		r.Get("/api/operator/queue", s.handleOperatorQueue)
		r.Get("/api/operator/appeals", s.handleOperatorAppeals)
		// ТЗ 5.1: жалоба уходит оператору; эксперт её не видит.
		r.Get("/api/operator/complaints", s.handleAdminComplaints)
	})

	r.Group(func(r chi.Router) {
		r.Use(s.requireRole(domain.RoleExpert, domain.RoleAdmin))
		r.Get("/api/expert/appeals", s.handleExpertAppeals)
	})

	r.Group(func(r chi.Router) {
		r.Use(s.requireRole(domain.RoleAdmin))
		r.Get("/api/admin/appeals", s.handleAdminAppeals)
		r.Get("/api/admin/settings", s.handleAdminGetSettings)
		r.Put("/api/admin/settings", s.handleAdminUpdateSettings)
		r.Get("/api/admin/users", s.handleAdminListUsers)
		r.Post("/api/admin/users", s.handleAdminCreateUser)
		r.Patch("/api/admin/users/{userID}", s.handleAdminPatchUser)
		r.Get("/api/admin/categories", s.handleAdminListCategories)
		r.Post("/api/admin/categories", s.handleAdminCreateCategory)
		r.Patch("/api/admin/categories/{categoryID}", s.handleAdminPatchCategory)
		r.Get("/api/admin/complaints", s.handleAdminComplaints)
		r.Get("/api/admin/stats", s.handleAdminStats)
	})
	r.Route("/api/appeals/{appealID}", func(r chi.Router) {
		r.Use(s.requireStaff)
		r.Get("/", s.handleStaffGetAppeal)
		r.Get("/messages", s.handleStaffMessages)
		r.Post("/messages", s.handleStaffPostMessage)
		r.Get("/notes", s.handleStaffNotes)
		r.Post("/notes", s.handleStaffPostNote)
		r.Get("/events", s.handleStaffEvents)
		r.Get("/presence", s.handlePresenceGet)
		r.Post("/presence", s.handlePresencePost)
		r.Post("/attachments", s.handleUploadAttachment)
		r.Get("/attachments/{attachmentID}", s.handleDownloadAttachment)
		r.Post("/assign", s.handleAssign)
		r.Post("/reject", s.handleReject)
		r.Post("/complete", s.handleCompleteByOperator)
		r.Post("/priority", s.handleSetPriority)
		r.Post("/category", s.handleSetCategory)
		r.Post("/take", s.handleTakeInProgress)
		r.Post("/clarify", s.handleClarify)
		r.Post("/recommendation", s.handlePublishRecommendation)
		r.Post("/transfer-request", s.handleRequestTransfer)
		r.Post("/close-no-response", s.handleCloseNoResponse)
		r.Post("/return", s.handleReturnForRework)
		r.Post("/contributors", s.handleAddContributor)
		r.Post("/admin-status", s.handleAdminSetStatus)
		r.Post("/crisis-flag", s.handleSetCrisisFlag)
	})

	return r
}

type principalCtxKey struct{}

// Публичный порт слушает только куки заявителей, служебный — только сессии
// сотрудников (HttpOnly-кука; bearer в теле логина больше не выдаётся).
func (s *Server) authMW(allowStaff, allowApplicant bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var p domain.Principal
			found := false

			if allowStaff {
				if c, err := r.Cookie(staffCookie); err == nil && c.Value != "" {
					hash := hashToken(c.Value)
					userID, ok, err := s.st.GetStaffSession(r.Context(), hash)
					if err == nil && ok {
						if u, err := s.st.GetUserByID(r.Context(), userID); err == nil && u.Active {
							p = domain.Principal{Role: domain.Role(u.Role), UserID: u.ID, Login: u.Login, MustChangePassword: u.MustChangePassword}
							found = true
						}
					}
				}
			}
			if !found && allowApplicant {
				if c, err := r.Cookie(applicantCookie); err == nil && c.Value != "" {
					hash := hashToken(c.Value)
					appealID, uaHash, expires, ok, err := s.st.GetApplicantSession(r.Context(), hash)
					if err != nil {
						slog.Error("applicant session lookup", "error", err)
					} else if ok {
						// Привязка сессии к устройству: трек-номер и кука,
						// уведённые с одного экрана, на другом браузере не работают.
						if uaHash == applicantUAHash(r) {
							p = domain.Principal{Role: domain.RoleApplicant, AppealID: appealID}
							found = true
							// Постепенный refresh: активному заявителю, у которого
							// до истечения сессии осталось меньше половины TTL,
							// выдаём свежий токен (старый удаляется).
							if time.Until(expires) < s.cfg.SessionTTL/2 {
								s.rotateApplicantSession(w, r, hash, appealID)
							}
						} else {
							// Кто-то пытается пользоваться сессией с другого
							// устройства — это может быть и уведённая кука.
							slog.Warn("applicant session: user-agent mismatch",
								"appeal_id", appealID, "remote", clientIP(r))
						}
					}
				}
			}

			if found {
				r = r.WithContext(context.WithValue(r.Context(), principalCtxKey{}, p))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func principalFrom(ctx context.Context) (domain.Principal, bool) {
	p, ok := ctx.Value(principalCtxKey{}).(domain.Principal)
	return p, ok
}

func (s *Server) requireApplicant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := principalFrom(r.Context()); ok && p.Role == domain.RoleApplicant {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusUnauthorized, errorResp{"track number verification required"})
	})
}

func (s *Server) requireStaff(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := principalFrom(r.Context()); ok && p.IsStaff() {
			// Демо-пароль должен быть сменён: до смены доступен только POST /api/auth/password.
			if p.MustChangePassword && r.URL.Path != "/api/auth/password" {
				writeJSON(w, http.StatusForbidden, errorResp{"password change required"})
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusUnauthorized, errorResp{"staff authentication required"})
	})
}

func (s *Server) requireRole(roles ...domain.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := principalFrom(r.Context())
			if !ok || !p.IsStaff() {
				writeJSON(w, http.StatusUnauthorized, errorResp{"staff authentication required"})
				return
			}
			// Демо-пароль должен быть сменён до работы с API (кроме самой смены пароля).
			if p.MustChangePassword && r.URL.Path != "/api/auth/password" {
				writeJSON(w, http.StatusForbidden, errorResp{"password change required"})
				return
			}
			for _, role := range roles {
				if p.Role == role {
					next.ServeHTTP(w, r)
					return
				}
			}
			writeJSON(w, http.StatusForbidden, errorResp{"insufficient permissions"})
		})
	}
}

// rlMaxKeys — верхняя граница числа ключей рейт-лимитера: защита памяти от
// атаки с ротацией адресов (карта не может расти бесконечно).
const rlMaxKeys = 1 << 16

type rateLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newRateLimiter() *rateLimiter {
	rl := &rateLimiter{hits: map[string][]time.Time{}}
	go rl.sweeper()
	return rl
}

// sweeper периодически удаляет «остывшие» ключи, по которым давно не было
// обращений. Без него карта росла бы неограниченно на пуле меняющихся IP.
func (rl *rateLimiter) sweeper() {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for range t.C {
		rl.sweep(time.Hour)
	}
}

func (rl *rateLimiter) sweep(window time.Duration) {
	now := time.Now()
	for k, v := range rl.hits {
		fresh := v[:0]
		for _, ts := range v {
			if now.Sub(ts) < window {
				fresh = append(fresh, ts)
			}
		}
		if len(fresh) == 0 {
			delete(rl.hits, k)
		} else {
			rl.hits[k] = fresh
		}
	}
}

func (rl *rateLimiter) allow(key string, n int, window time.Duration) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	kept := rl.hits[key][:0]
	for _, t := range rl.hits[key] {
		if now.Sub(t) < window {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(rl.hits, key)
	}
	if len(kept) >= n {
		rl.hits[key] = kept
		return false
	}
	// Новый ключ при исчерпанном лимите ключей вытесняет самый давно
	// использовавшийся. Прежний вариант (отказывать новичку) означал бы
	// лёгкий DoS: атакующий с ротой IP заполнял карту — и легитимные
	// посетители не могли ни войти, ни создать обращение.
	if _, exists := rl.hits[key]; !exists && len(rl.hits) >= rlMaxKeys {
		victim, last := "", time.Now()
		for k, v := range rl.hits {
			kt := time.Time{}
			if len(v) > 0 {
				kt = v[len(v)-1]
			}
			if victim == "" || kt.Before(last) {
				victim, last = k, kt
			}
		}
		delete(rl.hits, victim)
	}
	rl.hits[key] = append(kept, now)
	return true
}

// reset вызывается после успешной аутентификации: прошлые опечатки
// не должны держать легитимного владельца в блокировке.
func (rl *rateLimiter) reset(key string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.hits, key)
}

// clientIP возвращает адрес клиента для рейт-лимитов. Приложение стоит за
// Caddy в закрытой docker-сети, поэтому X-Forwarded-For учитывается только
// когда соединение пришло от приватного/loopback-адреса (то есть от нашего
// прокси). Запрос «напрямую из интернета» с собственным XFF доверия не имеет —
// иначе злоумышленник подменял бы заголовок и обходил лимиты перебора.
// Берём последний элемент списка: его дописал доверенный прокси (клиент
// контролирует только начало списка).
func clientIP(r *http.Request) string {
	remote := remoteAddrHost(r.RemoteAddr)
	if !isTrustedProxyAddr(remote) {
		return remote
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return remote
	}
	parts := strings.Split(xff, ",")
	last := strings.TrimSpace(parts[len(parts)-1])
	if host, _, err := net.SplitHostPort(last); err == nil {
		last = host
	}
	if net.ParseIP(last) == nil {
		return remote
	}
	return last
}

func remoteAddrHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func isTrustedProxyAddr(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

type errorResp struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrUnauthorized):
		writeJSON(w, http.StatusUnauthorized, errorResp{err.Error()})
	case errors.Is(err, domain.ErrForbidden):
		writeJSON(w, http.StatusForbidden, errorResp{err.Error()})
	case errors.Is(err, domain.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errorResp{"not found"})
	case errors.Is(err, domain.ErrConflict):
		writeJSON(w, http.StatusConflict, errorResp{"state conflict"})
	case errors.Is(err, domain.ErrValidation):
		writeJSON(w, http.StatusUnprocessableEntity, errorResp{"validation error"})
	case errors.Is(err, domain.ErrRateLimited):
		writeJSON(w, http.StatusTooManyRequests, errorResp{"rate limited"})
	case errors.Is(err, domain.ErrReturnLimitReached):
		// ТЗ 5.1: лимит возвратов исчерпан — текст для заявителя.
		writeJSON(w, http.StatusConflict, errorResp{"достигнут лимит возвратов: обращение можно завершить, оценить работу или отправить жалобу"})
	default:
		slog.Error("httpapi: internal error",
			"request_id", middleware.GetReqID(r.Context()),
			"error", err.Error())
		writeJSON(w, http.StatusInternalServerError, errorResp{"internal error"})
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResp{"invalid JSON body"})
		return false
	}
	return true
}

func parseUUID(s string) (uuid.UUID, bool) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	return id, err == nil
}
