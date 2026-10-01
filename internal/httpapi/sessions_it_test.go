//go:build integration

// Аудит №9 (привязка сессии заявителя к устройству + ротация токена)
// и №11 (пагинация операторской очереди).
package httpapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIT_ApplicantSessionDeviceBinding(t *testing.T) {
	e := newIT(t)
	appealID, track := e.createAppeal("Свидетель привязки сессии к устройству", "")

	phoneUA := "Mozilla/5.0 (Linux; Android 14) Mobile"
	doWithUA := func(method, path string, cookie *http.Cookie, body any, ua string) *httptest.ResponseRecorder {
		var rd *bytes.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			rd = bytes.NewReader(raw)
		} else {
			rd = bytes.NewReader(nil)
		}
		r := httptest.NewRequest(method, path, rd)
		r.Header.Set("User-Agent", ua)
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		e.pub.ServeHTTP(w, r)
		return w
	}

	// Вход «с телефона».
	w := doWithUA("POST", "/api/appeals/track", nil, map[string]string{"track_number": track}, phoneUA)
	if w.Code != http.StatusOK {
		t.Fatalf("verify track: %d %s", w.Code, w.Body.String())
	}
	var ac *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == applicantCookieName {
			ac = c
		}
	}
	if ac == nil {
		t.Fatal("кука сессии заявителя не выдана")
	}

	// Тот же браузер — работает.
	if w := doWithUA("GET", "/api/appeals/me/", ac, nil, phoneUA); w.Code != http.StatusOK {
		t.Errorf("тот же UA: %d, want 200", w.Code)
	}
	// Чужой браузер с той же кукой — нет (умышленная передача трека/куки).
	if w := doWithUA("GET", "/api/appeals/me/", ac, nil, "Mozilla/5.0 (Windows NT 10.0) Chrome/126"); w.Code != http.StatusUnauthorized {
		t.Errorf("чужой UA: %d, want 401", w.Code)
	}

	// Ротация: до истечения осталось меньше половины TTL (TTL теста — 1 ч) —
	// подвигаем expires_at, следующий запрос должен выдать новую куку.
	if _, err := e.st.DB.ExecContext(context.Background(),
		`UPDATE applicant_sessions SET expires_at = now() + interval '10 minutes' WHERE appeal_id = $1`, appealID); err != nil {
		t.Fatal(err)
	}
	w = doWithUA("GET", "/api/appeals/me/", ac, nil, phoneUA)
	if w.Code != http.StatusOK {
		t.Fatalf("после сдвига expires: %d", w.Code)
	}
	var rotated *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == applicantCookieName && c.Value != ac.Value {
			rotated = c
		}
	}
	if rotated == nil {
		t.Fatal("ротация: новая кука не выдана")
	}
	// Старый токен отозван.
	if w := doWithUA("GET", "/api/appeals/me/", ac, nil, phoneUA); w.Code != http.StatusUnauthorized {
		t.Errorf("старый токен должен быть отозван: %d", w.Code)
	}
	// Новый работает.
	if w := doWithUA("GET", "/api/appeals/me/", rotated, nil, phoneUA); w.Code != http.StatusOK {
		t.Errorf("новый токен: %d, want 200", w.Code)
	}

	// Сессии, созданные до миграции (ua_hash = ''), больше не признаются.
	h := sha256.Sum256([]byte("legacy-token"))
	legacy := hex.EncodeToString(h[:])
	if _, err := e.st.DB.ExecContext(context.Background(),
		`INSERT INTO applicant_sessions (token_hash, appeal_id, expires_at, ua_hash)
		 VALUES ($1, $2, now() + interval '1 hour', '')`, legacy, appealID); err != nil {
		t.Fatal(err)
	}
	legacyCookie := &http.Cookie{Name: applicantCookieName, Value: "legacy-token"}
	if w := doWithUA("GET", "/api/appeals/me/", legacyCookie, nil, phoneUA); w.Code != http.StatusUnauthorized {
		t.Errorf("сессия без привязки не должна работать: %d", w.Code)
	}
}

// Пагинация операторской очереди: как у остальных списков — page/per_page
// с лимитом per_page и метаданными total/total_pages (+ overdue_total).
func TestIT_OperatorQueuePagination(t *testing.T) {
	e := newIT(t)
	opTok := e.login("operator")

	for _, desc := range []string{
		"Очередь, обращение первое для пагинации",
		"Очередь, обращение второе для пагинации",
		"Очередь, обращение третье для пагинации",
	} {
		e.createAppeal(desc, "")
	}

	w := e.mustDo(e.staff, "GET", "/api/operator/queue?page=1&per_page=2", opTok, nil, nil, http.StatusOK)
	var page1 struct {
		Appeals    []json.RawMessage `json:"appeals"`
		Page       int               `json:"page"`
		PerPage    int               `json:"per_page"`
		Total      int               `json:"total"`
		TotalPages int               `json:"total_pages"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page1); err != nil {
		t.Fatal(err)
	}
	if len(page1.Appeals) != 2 || page1.Page != 1 || page1.PerPage != 2 {
		t.Fatalf("страница 1: %+v (len=%d)", page1, len(page1.Appeals))
	}
	if page1.Total < 3 || page1.TotalPages < 2 {
		t.Fatalf("метаданные: total=%d total_pages=%d", page1.Total, page1.TotalPages)
	}
	if !strings.Contains(w.Body.String(), "overdue_total") {
		t.Errorf("нет overdue_total: %s", w.Body.String())
	}

	// Вторая страница существует и не пуста.
	e.mustDo(e.staff, "GET", "/api/operator/queue?page=2&per_page=2", opTok, nil, nil, http.StatusOK)

	// per_page > 100 и мусорные параметры отклоняются.
	for _, bad := range []string{"?per_page=101", "?page=0", "?page=abc"} {
		w = e.do(e.staff, "GET", "/api/operator/queue"+bad, opTok, nil, nil)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, w.Code)
		}
	}
}
