package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

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
				"default-src 'self'; script-src 'self' 'unsafe-inline'; "+
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

func baseRouter(cfg config.Config) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders(cfg))
	return r
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
	// Страницы сотрудника: вход, отдельная панель под каждую роль и карточка обращения.
	r.Get("/", s.pageFor("staff", "login.html")) // редирект на нужную панель делает JS
	r.Get("/login", s.pageFor("staff", "login.html"))
	r.Get("/operator", s.pageFor("staff", "operator.html"))
	r.Get("/expert", s.pageFor("staff", "expert.html"))
	r.Get("/admin", s.pageFor("staff", "admin.html"))
	r.Get("/detail", s.pageFor("staff", "detail.html"))
	r.Get("/detail/*", s.pageFor("staff", "detail.html"))
	r.Handle("/assets/*", assetsHandler())
	r.Get("/api/categories", s.handlePublicCategories)

	r.Post("/api/auth/login", s.handleLogin)
	r.Post("/api/auth/logout", s.handleLogout)
	r.With(s.requireStaff).Post("/api/auth/password", s.handleChangePassword)
	r.Get("/api/me", s.handleMe)

	// Общая группа вне ролевых: в chi поздняя регистрация перекрывает раннюю.
	r.Group(func(r chi.Router) {
		r.Use(s.requireRole(domain.RoleExpert, domain.RoleOperator, domain.RoleAdmin))
		r.Get("/api/staff/experts", s.handleListExperts)
		r.Get("/api/export/appeals", s.handleExportAppeals)
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
				if bt := bearerToken(r); bt != "" {
					hash := hashToken(bt)
					userID, ok, err := s.st.GetStaffSession(r.Context(), hash)
					if err == nil && ok {
						if u, err := s.st.GetUserByID(r.Context(), userID); err == nil && u.Active {
							p = domain.Principal{Role: domain.Role(u.Role), UserID: u.ID, Login: u.Login, MustChangePassword: u.MustChangePassword}
							found = true
						}
					}
				}
				if !found {
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
			}
			if !found && allowApplicant {
				if c, err := r.Cookie(applicantCookie); err == nil && c.Value != "" {
					hash := hashToken(c.Value)
					appealID, ok, err := s.st.GetApplicantSession(r.Context(), hash)
					if err == nil && ok {
						p = domain.Principal{Role: domain.RoleApplicant, AppealID: appealID}
						found = true
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

type rateLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{hits: map[string][]time.Time{}}
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

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type errorResp struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
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
		log.Printf("httpapi: internal error: %v", err)
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
