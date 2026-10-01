//go:build integration

// Сценарий безопасности из аудита №15: 2FA (TOTP) для сотрудников.
package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"otklik/internal/totp"
)

// Полный цикл 2FA — настройка, включение кодом, требование кода при
// логине, отключение паролем. Берём psychologist2: admin нужен другим
// тестам без кода; в конце таблица user_totp чистится принудительно.
func TestIT_TOTP(t *testing.T) {
	e := newIT(t)
	defer func() {
		_, _ = e.st.DB.ExecContext(context.Background(),
			`DELETE FROM user_totp WHERE user_id = (SELECT id FROM users WHERE login = 'psychologist2')`)
	}()

	tok := e.login("psychologist2")

	// Статус: выключена.
	w := e.mustDo(e.staff, "GET", "/api/auth/totp", tok, nil, nil, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("статус 2FA: %s", w.Body.String())
	}

	// Setup: секрет + otpauth-ссылка.
	w = e.mustDo(e.staff, "POST", "/api/auth/totp/setup", tok, nil, nil, http.StatusOK)
	var setup struct {
		Secret     string `json:"secret"`
		OTPAuthURL string `json:"otpauth_url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &setup); err != nil {
		t.Fatal(err)
	}
	if len(setup.Secret) != 32 || !strings.HasPrefix(setup.OTPAuthURL, "otpauth://totp/") {
		t.Fatalf("setup: %s", w.Body.String())
	}

	// Неверный код отклоняется.
	e.mustDo(e.staff, "POST", "/api/auth/totp/enable", tok, nil,
		map[string]string{"code": "432100"}, http.StatusBadRequest)

	// Верный код включает.
	code, err := totp.Code(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	e.mustDo(e.staff, "POST", "/api/auth/totp/enable", tok, nil,
		map[string]string{"code": code}, http.StatusOK)

	// Повторный setup заблокирован, пока 2FA включена.
	e.mustDo(e.staff, "POST", "/api/auth/totp/setup", tok, nil, nil, http.StatusConflict)

	// Логин без кода: 401 + totp_required (фронт по этому полю покажет ввод кода).
	w = e.do(e.staff, "POST", "/api/auth/login", "", nil,
		map[string]string{"login": "psychologist2", "password": itSeedPwd})
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "totp_required") {
		t.Fatalf("логин без кода: %d %s", w.Code, w.Body.String())
	}
	// Неверный код: тот же ответ.
	w = e.do(e.staff, "POST", "/api/auth/login", "", nil,
		map[string]string{"login": "psychologist2", "password": itSeedPwd, "totp_code": "654321"})
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "totp_required") {
		t.Fatalf("логин с неверным кодом: %d %s", w.Code, w.Body.String())
	}
	// Верный код: вход, флаг totp_enabled в ответе.
	code, _ = totp.Code(setup.Secret, time.Now())
	w = e.mustDo(e.staff, "POST", "/api/auth/login", "", nil,
		map[string]string{"login": "psychologist2", "password": itSeedPwd, "totp_code": code}, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"totp_enabled":true`) {
		t.Errorf("логин должен сообщать о включённой 2FA: %s", w.Body.String())
	}

	// Отключение: неверный пароль отклоняется.
	e.mustDo(e.staff, "POST", "/api/auth/totp/disable", tok, nil,
		map[string]string{"password": "wrong-password"}, http.StatusBadRequest)
	// Верный пароль отключает.
	e.mustDo(e.staff, "POST", "/api/auth/totp/disable", tok, nil,
		map[string]string{"password": itSeedPwd}, http.StatusOK)

	// Логин снова работает без кода.
	e.mustDo(e.staff, "POST", "/api/auth/login", "", nil,
		map[string]string{"login": "psychologist2", "password": itSeedPwd}, http.StatusOK)
}
