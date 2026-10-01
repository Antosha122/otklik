package httpapi

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"otklik/internal/totp"
)

// Второй фактор входа (TOTP, RFC 6238) для сотрудников. Флоу:
//  1. POST /api/auth/totp/setup — сервер генерирует секрет (пока выключенный)
//     и отдаёт его вместе с otpauth://-ссылкой для приложения-аутентификатора;
//  2. POST /api/auth/totp/enable {code} — пользователь подтверждает действующим
//     кодом, что добавил секрет в приложение, — проверка при логине включается;
//  3. логин с этого момента требует код в поле totp_code (без кода —
//     401 totp_required, фронт показывает поле);
//  4. POST /api/auth/totp/disable {password} — выключение (потеряли телефон —
//     отключает администратор БД, см. README).

func (s *Server) handleTOTPStatus(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	_, enabled, err := s.st.GetTOTPSecret(r.Context(), p.UserID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": enabled})
}

func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if _, enabled, err := s.st.GetTOTPSecret(r.Context(), p.UserID); err != nil {
		writeErr(w, r, err)
		return
	} else if enabled {
		writeJSON(w, http.StatusConflict, errorResp{"totp is already enabled: disable it first"})
		return
	}
	secret, err := totp.GenerateSecret()
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if err := s.st.SetTOTPSecret(r.Context(), p.UserID, secret); err != nil {
		writeErr(w, r, err)
		return
	}
	slog.Info("totp: setup started", "user_id", p.UserID, "login", p.Login)
	writeJSON(w, http.StatusOK, map[string]any{
		"secret":       secret,
		"otpauth_url":  totp.OTPAuthURL(secret, p.Login, "Otklik"),
		"instructions": "add the secret to your authenticator app, then confirm with a code",
	})
}

type totpCodeReq struct {
	Code string `json:"code"`
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var req totpCodeReq
	if !decodeJSON(w, r, &req) {
		return
	}
	secret, enabled, err := s.st.GetTOTPSecret(r.Context(), p.UserID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	if enabled {
		writeJSON(w, http.StatusConflict, errorResp{"totp is already enabled: disable it first"})
		return
	}
	if secret == "" {
		writeJSON(w, http.StatusBadRequest, errorResp{"run totp setup first"})
		return
	}
	// Перебор кодов ограничиваем отдельно от логина: код 6 цифр, окно ±30 c,
	// 10 попыток в минуту по учётной записи более чем достаточно.
	if !s.rl.allow("totp:"+p.Login, 10, time.Minute) {
		writeJSON(w, http.StatusTooManyRequests, errorResp{"too many attempts, try later"})
		return
	}
	if !totp.Verify(secret, req.Code, time.Now()) {
		writeJSON(w, http.StatusBadRequest, errorResp{"invalid totp code"})
		return
	}
	if _, err := s.st.EnableTOTP(r.Context(), p.UserID); err != nil {
		writeErr(w, r, err)
		return
	}
	slog.Info("totp: enabled", "user_id", p.UserID, "login", p.Login)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true})
}

type totpDisableReq struct {
	Password string `json:"password"`
}

// Отключение требует текущий пароль: кода у пользователя может уже не быть
// (потерян телефон), а пароль — то, что знает только владелец учётной записи.
func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var req totpDisableReq
	if !decodeJSON(w, r, &req) {
		return
	}
	u, hash, err := s.st.GetUserByLogin(r.Context(), p.Login)
	if err != nil || u.ID != p.UserID ||
		bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		if !s.rl.allow("login:"+clientIP(r), 10, time.Minute) {
			writeJSON(w, http.StatusTooManyRequests, errorResp{"too many attempts, try later"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResp{"invalid password"})
		return
	}
	if err := s.st.DeleteTOTP(r.Context(), p.UserID); err != nil {
		writeErr(w, r, err)
		return
	}
	slog.Info("totp: disabled", "user_id", p.UserID, "login", p.Login)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
}

// checkTOTPAtLogin — проверка второго фактора после успешного пароля.
// Возвращает true, если логин можно продолжать. Ответ (401 + totp_required)
// уже написан в w при ошибке.
func (s *Server) checkTOTPAtLogin(w http.ResponseWriter, r *http.Request, userID uuid.UUID, login, code string) bool {
	secret, enabled, err := s.st.GetTOTPSecret(r.Context(), userID)
	if err != nil {
		writeErr(w, r, err)
		return false
	}
	if !enabled {
		return true
	}
	code = strings.TrimSpace(code)
	if code == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "totp code required", "totp_required": true})
		return false
	}
	// Тот же лимит, что и в enable: защита от подбора 6-значного кода.
	if !s.rl.allow("totp:"+login, 10, time.Minute) {
		writeJSON(w, http.StatusTooManyRequests, errorResp{"too many attempts, try later"})
		return false
	}
	if !totp.Verify(secret, code, time.Now()) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid totp code", "totp_required": true})
		return false
	}
	return true
}
