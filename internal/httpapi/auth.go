package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"otklik/internal/domain"
)

func newToken() (token, tokenHash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	token = hex.EncodeToString(buf)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (s *Server) setCookie(w http.ResponseWriter, name, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
	})
}

func (s *Server) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/", HttpOnly: true,
		Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

type loginReq struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Login = strings.TrimSpace(req.Login)
	if req.Login == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, errorResp{"login and password are required"})
		return
	}
	u, hash, err := s.st.GetUserByLogin(r.Context(), req.Login)
	if err == nil {
		err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password))
	}
	if err != nil {
		if !s.rl.allow("login:"+clientIP(r), 10, time.Minute) {
			writeJSON(w, http.StatusTooManyRequests, errorResp{"too many attempts, try later"})
			return
		}
		writeJSON(w, http.StatusUnauthorized, errorResp{"invalid credentials"})
		return
	}
	s.rl.reset("login:" + clientIP(r))
	if !u.Active {
		writeJSON(w, http.StatusForbidden, errorResp{"user is deactivated"})
		return
	}

	// Флаг обязательной смены сверяется с фактом: пароль уже не демо-пароль
	// (например, хеш меняли руками, а флаг остался) — требовать смену нельзя.
	// Демо-пароль всё ещё стоит — смена обязательна один раз, до фактической замены.
	if u.MustChangePassword && s.cfg.SeedDefaultPwd != "" &&
		bcrypt.CompareHashAndPassword([]byte(hash), []byte(s.cfg.SeedDefaultPwd)) != nil {
		if err := s.st.SetMustChangePassword(r.Context(), u.ID, false); err != nil {
			writeErr(w, r, err)
			return
		}
		u.MustChangePassword = false
	}

	token, tokenHash, err := newToken()
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := s.st.CreateStaffSession(r.Context(), tokenHash, u.ID, time.Now().Add(s.cfg.SessionTTL)); err != nil {
		writeErr(w, r, err)
		return
	}
	s.setCookie(w, staffCookie, token)
	// Токен живёт только в HttpOnly-куке: дублирование в теле ответа убрано,
	// фронтенд ходит исключительно через cookie-сессию.
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":              u.ID,
		"login":                u.Login,
		"role":                 u.Role,
		"specialist_group":     u.SpecialistGroup,
		"active":               u.Active,
		"must_change_password": u.MustChangePassword,
	})
}

// staffSessionToken достаёт токен сессии исключительно из HttpOnly-куки:
// bearer-токены больше не выдаются и не принимаются.
func staffSessionToken(r *http.Request) string {
	if c, err := r.Cookie(staffCookie); err == nil && c.Value != "" {
		return c.Value
	}
	return ""
}

type changePasswordReq struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// handleChangePassword: сотрудник меняет свой пароль, зная текущий.
// Все прочие сессии пользователя инвалидируются, текущая — остаётся.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFrom(r.Context())
	if !ok || !p.IsStaff() {
		writeJSON(w, http.StatusUnauthorized, errorResp{"staff authentication required"})
		return
	}
	var req changePasswordReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		writeJSON(w, http.StatusBadRequest, errorResp{"current_password and new_password are required"})
		return
	}
	if !s.rl.allow("chpwd:"+p.Login, 5, time.Minute) {
		writeJSON(w, http.StatusTooManyRequests, errorResp{"too many attempts, try later"})
		return
	}

	_, hash, err := s.st.GetUserByLogin(r.Context(), p.Login)
	if err == nil {
		err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.CurrentPassword))
	}
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, errorResp{"invalid credentials"})
		return
	}
	s.rl.reset("chpwd:" + p.Login)

	if len(req.NewPassword) < 8 {
		writeJSON(w, http.StatusBadRequest, errorResp{"password must be at least 8 characters"})
		return
	}
	if req.NewPassword == req.CurrentPassword {
		writeJSON(w, http.StatusBadRequest, errorResp{"new password must differ from current"})
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := s.st.UpdateUserPassword(r.Context(), p.UserID, string(newHash)); err != nil {
		writeErr(w, r, err)
		return
	}

	// Смена пароля — повод выкинуть прочие сессии (другие вкладки/устройства).
	currentHash := hashToken(staffSessionToken(r))
	if err := s.st.DeleteStaffSessionsForUser(r.Context(), p.UserID, currentHash); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "password changed"})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if tok := staffSessionToken(r); tok != "" {
		_ = s.st.DeleteStaffSession(r.Context(), hashToken(tok))
	}
	s.clearCookie(w, staffCookie)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

func (s *Server) handleApplicantLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(applicantCookie); err == nil && c.Value != "" {
		_ = s.st.DeleteApplicantSession(r.Context(), hashToken(c.Value))
	}
	s.clearCookie(w, applicantCookie)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFrom(r.Context())
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"role": "anonymous"})
		return
	}
	resp := map[string]any{"role": p.Role}
	if p.IsStaff() {
		resp["login"] = p.Login
		resp["user_id"] = p.UserID
		resp["must_change_password"] = p.MustChangePassword
	} else {
		resp["appeal_id"] = p.AppealID
	}
	writeJSON(w, http.StatusOK, resp)
}

func actorPtr(p domain.Principal) *uuid.UUID {
	if !p.IsStaff() {
		return nil
	}
	id := p.UserID
	return &id
}
