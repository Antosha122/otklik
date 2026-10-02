//go:build integration

// Интеграционные сценарии против чистой PostgreSQL-базы otklik_it_test.
// Запуск: go test -tags integration ./internal/httpapi
// URL базы переопределяется переменной OTKLIK_IT_DATABASE_URL.
package httpapi_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"otklik/internal/config"
	"otklik/internal/db"
	"otklik/internal/httpapi"
	"otklik/internal/store"
)

const (
	itSeedPwd           = "it-strong-pwd-42" // без «otklik»: блоклист слабых паролей запрещает имя сервиса
	applicantCookieName = "otklik_applicant"
)

var itDB *sql.DB

func itDatabaseURL() string {
	if u := os.Getenv("OTKLIK_IT_DATABASE_URL"); u != "" {
		return u
	}
	// compose публикует БД наружу контейнера только на 127.0.0.1:5433.
	return "postgres://otklik:otklik@localhost:5433/otklik_it_test?sslmode=disable"
}

// itSetup один раз за прогон пересоздаёт схему, накатывает миграции и сид.
func itSetup(t *testing.T) *sql.DB {
	t.Helper()
	if itDB != nil {
		return itDB
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d, err := db.Open(ctx, itDatabaseURL())
	if err != nil {
		t.Fatalf("не удалось подключиться к тестовой БД %s: %v (поднять: docker compose up -d db)", itDatabaseURL(), err)
	}
	if _, err := d.ExecContext(ctx, `DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if err := db.Migrate(ctx, d); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Seed(ctx, d, itSeedPwd); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Миграция 007 помечает демо-учётки must_change_password=true —
	// для большинства сценариев флаг снимаем; отдельный тест проверяет его.
	if _, err := d.ExecContext(ctx, `UPDATE users SET must_change_password = false`); err != nil {
		t.Fatalf("reset must_change_password: %v", err)
	}
	itDB = d
	return d
}

// Демо-пароль: пока must_change_password=true, API сотрудника закрыт (403),
// кроме POST /api/auth/password; смена пароля открывает доступ обратно.
func TestIT_MustChangePassword(t *testing.T) {
	e := newIT(t)
	if _, err := e.st.DB.ExecContext(context.Background(),
		`UPDATE users SET must_change_password = true WHERE login = 'operator'`); err != nil {
		t.Fatal(err)
	}

	w := e.do(e.staff, "POST", "/api/auth/login", "", nil,
		map[string]string{"login": "operator", "password": itSeedPwd})
	if w.Code != http.StatusOK {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"must_change_password":true`) {
		t.Errorf("логин должен сообщать о необходимости смены пароля: %s", w.Body.String())
	}
	tok := e.staffTokenOf(w)

	w = e.do(e.staff, "GET", "/api/operator/queue", tok, nil, nil)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "password change required") {
		t.Errorf("до смены пароля API должен быть закрыт: %d %s", w.Code, w.Body.String())
	}

	w = e.do(e.staff, "POST", "/api/auth/password", tok, nil,
		map[string]string{"current_password": itSeedPwd, "new_password": "new-strong-pwd-42"})
	if w.Code != http.StatusOK {
		t.Fatalf("change password: %d %s", w.Code, w.Body.String())
	}

	w = e.do(e.staff, "GET", "/api/operator/queue", tok, nil, nil)
	if w.Code != http.StatusOK {
		t.Errorf("после смены пароля API должен открыться: %d %s", w.Code, w.Body.String())
	}

	// Возвращаем исходный пароль: последующие тесты логинятся оператором с itSeedPwd.
	w = e.do(e.staff, "POST", "/api/auth/password", tok, nil,
		map[string]string{"current_password": "new-strong-pwd-42", "new_password": itSeedPwd})
	if w.Code != http.StatusOK {
		t.Fatalf("restore password: %d %s", w.Code, w.Body.String())
	}
}

// Сверка флага обязательной смены с фактом при логине: если пароль уже
// НЕ демо-пароль, повторных требований смены быть не должно — даже если
// флаг кем-то выставлен заново (ручной сброс хеша, перенос БД и т.п.).
func TestIT_MustChangePasswordReconcile(t *testing.T) {
	e := newIT(t)

	// Пароль ещё демо-пароль → смена обязательна (флаг остаётся true).
	if _, err := e.st.DB.ExecContext(context.Background(),
		`UPDATE users SET must_change_password = true WHERE login = 'lawyer1'`); err != nil {
		t.Fatal(err)
	}
	w := e.do(e.staff, "POST", "/api/auth/login", "", nil,
		map[string]string{"login": "lawyer1", "password": itSeedPwd})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"must_change_password":true`) {
		t.Fatalf("демо-пароль требует смены: %d %s", w.Code, w.Body.String())
	}

	// Сотрудник меняет пароль на свой...
	tok := e.staffTokenOf(w)
	w = e.mustDo(e.staff, "POST", "/api/auth/password", tok, nil,
		map[string]string{"current_password": itSeedPwd, "new_password": "my-own-strong-pwd"}, http.StatusOK)

	// ...а флаг «вдруг» снова true (имитация ручного сброса/переноса).
	if _, err := e.st.DB.ExecContext(context.Background(),
		`UPDATE users SET must_change_password = true WHERE login = 'lawyer1'`); err != nil {
		t.Fatal(err)
	}
	w = e.do(e.staff, "POST", "/api/auth/login", "", nil,
		map[string]string{"login": "lawyer1", "password": "my-own-strong-pwd"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"must_change_password":false`) {
		t.Fatalf("свой пароль не должен требовать смены: %d %s", w.Code, w.Body.String())
	}
	tok = e.staffTokenOf(w)
	// Флаг погашен и в БД: API открыт без повторного диалога.
	e.mustDo(e.staff, "GET", "/api/profile", tok, nil, nil, http.StatusOK)

	// Возвращаем демо-пароль: последующие тесты логинятся lawyer1 с itSeedPwd.
	e.mustDo(e.staff, "POST", "/api/auth/password", tok, nil,
		map[string]string{"current_password": "my-own-strong-pwd", "new_password": itSeedPwd}, http.StatusOK)
}

// itEnv — изолированные HTTP-инстансы (свежие rate-limiter'ы) поверх общей БД.
type itEnv struct {
	t     *testing.T
	st    *store.Store
	pub   http.Handler
	staff http.Handler
}

func newIT(t *testing.T) *itEnv {
	t.Helper()
	d := itSetup(t)
	st := store.New(d)
	cfg := config.Config{
		SessionTTL: time.Hour,
		// 2FA в интеграционных тестах включена: сценарий TestIT_TOTP
		// проверяет полный цикл setup→enable→login.
		TotpEnabled:    true,
		AttachmentsDir: t.TempDir(),
		// Демо-пароль сервера совпадает с сид-паролём: тест обязательной смены
		// и сверка флага с фактом работают на тех же данных.
		SeedDefaultPwd: itSeedPwd,
	}
	return &itEnv{
		t:     t,
		st:    st,
		pub:   httpapi.New(cfg, st),
		staff: httpapi.NewStaff(cfg, st),
	}
}

// staffCookieName — имя HttpOnly-куки сессии сотрудника (токен в теле логина не выдаётся).
const staffCookieName = "otklik_staff"

// do отправляет запрос. staffTok — значение куки сессии сотрудника;
// cookie — произвольная кука (используется для сессии заявителя).
func (e *itEnv) do(h http.Handler, method, path, staffTok string, cookie *http.Cookie, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rd)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if staffTok != "" {
		r.AddCookie(&http.Cookie{Name: staffCookieName, Value: staffTok})
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func (e *itEnv) mustDo(h http.Handler, method, path, staffTok string, cookie *http.Cookie, body any, wantCode int) *httptest.ResponseRecorder {
	e.t.Helper()
	w := e.do(h, method, path, staffTok, cookie, body)
	if w.Code != wantCode {
		e.t.Fatalf("%s %s: код = %d, want %d, тело: %s", method, path, w.Code, wantCode, w.Body.String())
	}
	return w
}

func (e *itEnv) login(login string) string {
	e.t.Helper()
	w := e.mustDo(e.staff, "POST", "/api/auth/login", "", nil,
		map[string]string{"login": login, "password": itSeedPwd}, http.StatusOK)

	// Токен сессии сотрудника живёт только в HttpOnly-куке.
	for _, c := range w.Result().Cookies() {
		if c.Name == staffCookieName {
			if !c.HttpOnly {
				e.t.Error("кука сессии сотрудника должна быть HttpOnly")
			}
			if c.SameSite != http.SameSiteLaxMode {
				e.t.Errorf("SameSite = %v, want Lax", c.SameSite)
			}
			if c.Path != "/" || c.MaxAge != 3600 {
				e.t.Errorf("кука сессии: Path=%q MaxAge=%d, want \"/\" и 3600", c.Path, c.MaxAge)
			}
		}
	}
	return e.staffTokenOf(w)
}

// staffTokenOf достаёт из ответа значение куки сессии сотрудника
// и заодно проверяет, что токен не утёк в тело ответа.
func (e *itEnv) staffTokenOf(w *httptest.ResponseRecorder) string {
	e.t.Helper()
	if strings.Contains(w.Body.String(), "session_token") {
		e.t.Errorf("тело логина не должно содержать session_token: %s", w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == staffCookieName {
			return c.Value
		}
	}
	e.t.Fatal("кука сессии сотрудника не выдана")
	return ""
}

// createAppeal создаёт анонимное обращение и возвращает (appealID, трек-номер).
func (e *itEnv) createAppeal(desc string, crisisContact string) (string, string) {
	e.t.Helper()
	w := e.mustDo(e.pub, "POST", "/api/appeals", "", nil,
		map[string]any{
			"applicant_type": "schoolchild",
			"category_id":    e.categoryID(),
			"description":    desc,
			"crisis_contact": crisisContact,
		}, http.StatusCreated)
	var out struct {
		AppealID    string `json:"appeal_id"`
		TrackNumber string `json:"track_number"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		e.t.Fatal(err)
	}
	return out.AppealID, out.TrackNumber
}

func (e *itEnv) applicantCookie(track string) *http.Cookie {
	e.t.Helper()
	w := e.mustDo(e.pub, "POST", "/api/appeals/track", "", nil,
		map[string]string{"track_number": track}, http.StatusOK)
	for _, c := range w.Result().Cookies() {
		if c.Name == applicantCookieName {
			if !c.HttpOnly {
				e.t.Error("кука сессии заявителя должна быть HttpOnly")
			}
			return c
		}
	}
	e.t.Fatal("кука сессии заявителя не выдана")
	return nil
}

func (e *itEnv) categoryID() string {
	e.t.Helper()
	w := e.mustDo(e.pub, "GET", "/api/categories", "", nil, nil, http.StatusOK)
	var out struct {
		Categories []struct {
			ID uuid.UUID `json:"id"`
		} `json:"categories"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Categories) == 0 {
		e.t.Fatalf("категории не получены: %v %s", err, w.Body.String())
	}
	return out.Categories[0].ID.String()
}

func (e *itEnv) expertID(login string) string {
	e.t.Helper()
	users, err := e.st.ListUsers(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	for _, u := range users {
		if u.Login == login && u.Role == "expert" {
			return u.ID.String()
		}
	}
	e.t.Fatalf("эксперт %s не найден в сидах", login)
	return ""
}

// toAnswerReady проводит обращение по пути new→assigned→in_progress→answer_ready.
func (e *itEnv) toAnswerReady(appealID string) (opTok, expTok string) {
	e.t.Helper()
	opTok = e.login("operator")
	expTok = e.login("psychologist1")
	e.mustDo(e.staff, "POST", "/api/appeals/"+appealID+"/assign", opTok, nil,
		map[string]string{"expert_id": e.expertID("psychologist1")}, http.StatusOK)
	e.mustDo(e.staff, "POST", "/api/appeals/"+appealID+"/take", expTok, nil, nil, http.StatusOK)
	e.mustDo(e.staff, "POST", "/api/appeals/"+appealID+"/recommendation", expTok, nil,
		map[string]string{"recommendation": "Рекомендация: обратиться к школьному психологу"}, http.StatusOK)
	return opTok, expTok
}

func appealStatus(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var a struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
		t.Fatalf("не удалось разобрать ответ: %v %s", err, w.Body.String())
	}
	return a.Status
}

// С1–С3: подача → маршрутизация → работа эксперта → результат заявителя.
func TestIT_AcceptanceHappyPath(t *testing.T) {
	e := newIT(t)
	appealID, track := e.createAppeal("Меня обижают в классе уже второй месяц, помогите пожалуйста", "")

	// Заявитель входит по трек-номеру и видит объяснение статуса.
	w := e.mustDo(e.pub, "GET", "/api/appeals/me/", "", e.applicantCookie(track), nil, http.StatusOK)
	if got := appealStatus(t, w); got != "new" {
		t.Fatalf("статус после создания = %q, want new", got)
	}
	var view struct {
		StatusExplanation string `json:"status_explanation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.StatusExplanation == "" {
		t.Error("объяснение статуса для заявителя пусто")
	}

	// Оператор видит обращение в очереди и назначает эксперта.
	opTok, _ := e.toAnswerReady(appealID)

	w = e.mustDo(e.staff, "GET", "/api/appeals/"+appealID+"/", opTok, nil, nil, http.StatusOK)
	if got := appealStatus(t, w); got != "answer_ready" {
		t.Fatalf("статус после рекомендации = %q, want answer_ready", got)
	}

	// Заявитель видит рекомендацию и подтверждает помощь.
	w = e.mustDo(e.pub, "GET", "/api/appeals/me/", "", e.applicantCookie(track), nil, http.StatusOK)
	if !strings.Contains(w.Body.String(), "Рекомендация: обратиться к школьному психологу") {
		t.Errorf("рекомендация не видна заявителю: %s", w.Body.String())
	}
	w = e.mustDo(e.pub, "POST", "/api/appeals/me/result", "", e.applicantCookie(track),
		map[string]any{"helped": true}, http.StatusOK)
	if got := appealStatus(t, w); got != "completed" {
		t.Fatalf("статус после «помогло» = %q, want completed", got)
	}

	// Оценка работы сохраняется.
	e.mustDo(e.pub, "POST", "/api/appeals/me/feedback", "", e.applicantCookie(track),
		map[string]any{"rating": 5, "comment": "спасибо"}, http.StatusOK)

	// В терминальном статусе чат закрыт.
	w = e.do(e.pub, "POST", "/api/appeals/me/messages", "", e.applicantCookie(track),
		map[string]string{"text": "ещё вопрос"})
	if w.Code != http.StatusConflict {
		t.Errorf("сообщение в закрытое обращение: код = %d, want 409", w.Code)
	}
}

// С4: уточняющий вопрос — заявитель отвечает в чате; жалоба уходит оператору;
// оператор закрывает обращение без ответа.
func TestIT_ClarificationChatAndComplaint(t *testing.T) {
	e := newIT(t)
	appealID, track := e.createAppeal("Произошёл конфликт с одноклассниками на переменке", "")

	opTok := e.login("operator")
	expTok := e.login("psychologist1")
	e.mustDo(e.staff, "POST", "/api/appeals/"+appealID+"/assign", opTok, nil,
		map[string]string{"expert_id": e.expertID("psychologist1")}, http.StatusOK)
	e.mustDo(e.staff, "POST", "/api/appeals/"+appealID+"/take", expTok, nil, nil, http.StatusOK)
	e.mustDo(e.staff, "POST", "/api/appeals/"+appealID+"/clarify", expTok, nil, nil, http.StatusOK)

	w := e.mustDo(e.pub, "GET", "/api/appeals/me/", "", e.applicantCookie(track), nil, http.StatusOK)
	if got := appealStatus(t, w); got != "needs_clarification" {
		t.Fatalf("статус = %q, want needs_clarification", got)
	}

	// Заявитель дополняет обращение — это тоже ответ: статус возвращается
	// к in_progress, у специалиста пропадает плашка «ожидаем ответа».
	ac := e.applicantCookie(track)
	w = e.mustDo(e.pub, "POST", "/api/appeals/me/append", "", ac,
		map[string]string{"text": "Драка случилась на уроке физкультуры, я не виноват"}, http.StatusOK)
	if got := appealStatus(t, w); got != "in_progress" {
		t.Errorf("после дополнения статус = %q, want in_progress", got)
	}

	// Заявитель дописывает в чат; сотрудник читает переписку.
	e.mustDo(e.pub, "POST", "/api/appeals/me/messages", "", ac,
		map[string]string{"text": "Это было на уроке физкультуры"}, http.StatusCreated)
	// Ответ в чате также возвращает статус к in_progress, если он ещё был needs_clarification.
	w = e.mustDo(e.pub, "GET", "/api/appeals/me/", "", ac, nil, http.StatusOK)
	if got := appealStatus(t, w); got != "in_progress" {
		t.Errorf("после ответа в чате статус = %q, want in_progress", got)
	}
	w = e.mustDo(e.staff, "GET", "/api/appeals/"+appealID+"/messages", expTok, nil, nil, http.StatusOK)
	if !strings.Contains(w.Body.String(), "на уроке физкультуры") {
		t.Errorf("сообщение заявителя не видно эксперту: %s", w.Body.String())
	}

	// Жалоба доступна оператору.
	e.mustDo(e.pub, "POST", "/api/appeals/me/complaint", "", ac,
		map[string]string{"text": "эксперт отвечал слишком долго"}, http.StatusCreated)
	e.mustDo(e.staff, "GET", "/api/operator/complaints", opTok, nil, nil, http.StatusOK)

	// Оператор закрывает обращение без ответа заявителя. Заявитель уже ответил,
	// поэтому сначала эксперт повторно запрашивает уточнение (возврат в needs_clarification).
	e.mustDo(e.staff, "POST", "/api/appeals/"+appealID+"/clarify", expTok, nil, nil, http.StatusOK)
	w = e.mustDo(e.staff, "POST", "/api/appeals/"+appealID+"/close-no-response", opTok, nil, nil, http.StatusOK)
	if got := appealStatus(t, w); got != "closed_no_response" {
		t.Fatalf("статус = %q, want closed_no_response", got)
	}
}

// С5: цикл возвратов ограничен настройкой max_returns (по умолчанию 2).
func TestIT_ReturnCycleLimit(t *testing.T) {
	e := newIT(t)
	appealID, track := e.createAppeal("Кибербуллинг в школьном чате, присылают обидные картинки", "")
	ac := e.applicantCookie(track)

	// Два полноценных цикла «не помогло».
	for i := 1; i <= 2; i++ {
		e.toAnswerReady(appealID)
		w := e.mustDo(e.pub, "POST", "/api/appeals/me/result", "", ac,
			map[string]any{"helped": false, "reason": "не помогло"}, http.StatusOK)
		if got := appealStatus(t, w); got != "returned" {
			t.Fatalf("цикл %d: статус = %q, want returned", i, got)
		}
	}

	// Третий возврат блокируется лимитом.
	e.toAnswerReady(appealID)
	w := e.do(e.pub, "POST", "/api/appeals/me/result", "", ac,
		map[string]any{"helped": false, "reason": "снова не помогло"})
	if w.Code != http.StatusConflict {
		t.Fatalf("третий возврат: код = %d, want 409, тело: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "лимит возвратов") {
		t.Errorf("заявитель должен получить объяснение про лимит: %s", w.Body.String())
	}

	// Обращение завершают жалобой — альтернатива по ТЗ 5.1.
	e.mustDo(e.pub, "POST", "/api/appeals/me/complaint", "", ac,
		map[string]string{"text": "передайте другому специалисту"}, http.StatusCreated)
}

// ТЗ «Кризисные обращения»: детект по маркерам, справочник помощи заявителю,
// контакт для связи виден оператору и не виден эксперту.
func TestIT_CrisisFlow(t *testing.T) {
	e := newIT(t)
	appealID, track := e.createAppeal("Я больше не могу, иногда не хочу жить, всё тяжело", "@tg: pupil_help")

	w := e.do(e.pub, "POST", "/api/appeals", "", nil,
		map[string]any{"applicant_type": "schoolchild", "category_id": e.categoryID(),
			"description": "просто конфликт, ничего серьёзного"})
	if w.Code != http.StatusCreated {
		t.Fatalf("повторное создание: код = %d", w.Code)
	}
	if strings.Contains(w.Body.String(), `"crisis_help"`) {
		t.Error("у некризисного обращения не должно быть crisis_help")
	}

	// У кризисного — справочник в ответе и во view заявителя.
	ac := e.applicantCookie(track)
	w = e.mustDo(e.pub, "GET", "/api/appeals/me/", "", ac, nil, http.StatusOK)
	if !strings.Contains(w.Body.String(), "8-800-2000-122") {
		t.Errorf("заявителю не показан телефон доверия: %s", w.Body.String())
	}

	opTok := e.login("operator")
	w = e.mustDo(e.staff, "GET", "/api/appeals/"+appealID+"/", opTok, nil, nil, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"crisis_detected":true`) {
		t.Errorf("оператору не виден флаг кризиса: %s", w.Body.String())
	}
	// Кризисное обращение автоматически получает срочный приоритет.
	if !strings.Contains(w.Body.String(), `"priority":"urgent"`) {
		t.Errorf("у кризисного обращения должен быть срочный приоритет: %s", w.Body.String())
	}
	// И понижать его нельзя.
	w = e.do(e.staff, "POST", "/api/appeals/"+appealID+"/priority", opTok, nil,
		map[string]string{"priority": "normal", "reason": "понизим"})
	if w.Code != http.StatusConflict {
		t.Errorf("понижение приоритета кризисного обращения должно давать 409, получили %d", w.Code)
	}

	// Контакт при кризисе виден в карточке обращения оператору.
	w = e.mustDo(e.staff, "GET", "/api/appeals/"+appealID+"/", opTok, nil, nil, http.StatusOK)
	if !strings.Contains(w.Body.String(), "@tg: pupil_help") {
		t.Errorf("оператору не виден контакт при кризисе: %s", w.Body.String())
	}

	// Эксперт видит кризис, но не контакт (ТЗ, п.4).
	expTok := e.login("psychologist1")
	e.mustDo(e.staff, "POST", "/api/appeals/"+appealID+"/assign", opTok, nil,
		map[string]string{"expert_id": e.expertID("psychologist1")}, http.StatusOK)
	w = e.mustDo(e.staff, "GET", "/api/appeals/"+appealID+"/", expTok, nil, nil, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"crisis_detected":true`) {
		t.Errorf("эксперту не виден флаг кризиса: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "@tg: pupil_help") {
		t.Error("эксперт не должен видеть контакт заявителя")
	}
}

// Матрица прав: у каждой роли — только свои действия.
func TestIT_RoleRights(t *testing.T) {
	e := newIT(t)
	appealID, _ := e.createAppeal("Нужна консультация по сложной ситуации в школе", "")
	opTok, expTok := e.toAnswerReady(appealID)
	admTok := e.login("admin")

	check := func(tok, method, path string, body any, wantCode int) {
		t.Helper()
		w := e.do(e.staff, method, path, tok, nil, body)
		if w.Code != wantCode {
			t.Errorf("%s %s: код = %d, want %d, тело: %s", method, path, w.Code, wantCode, w.Body.String())
		}
	}

	// Эксперт не оператор и не админ.
	check(expTok, "GET", "/api/operator/queue", nil, http.StatusForbidden)
	check(expTok, "GET", "/api/admin/stats", nil, http.StatusForbidden)
	// Оператор не эксперт и не админ.
	check(opTok, "GET", "/api/expert/appeals", nil, http.StatusForbidden)
	check(opTok, "GET", "/api/admin/users", nil, http.StatusForbidden)
	check(opTok, "POST", "/api/appeals/"+appealID+"/take", nil, http.StatusForbidden)
	// Админ: статистика доступна, но действия оператора — нет.
	check(admTok, "GET", "/api/admin/stats", nil, http.StatusOK)
	check(admTok, "POST", "/api/appeals/"+appealID+"/reject",
		map[string]string{"reason": "не по профилю"}, http.StatusForbidden)

	// Эксперт не отвечает за чужое обращение — доступ к карточке закрыт.
	other, _ := e.createAppeal("Другое обращение без назначения эксперта", "")
	check(expTok, "GET", "/api/appeals/"+other+"/", nil, http.StatusForbidden)

	// Эксперт-не-участник не может подключиться к переписке.
	check(e.login("lawyer1"), "POST", "/api/appeals/"+appealID+"/take", nil, http.StatusForbidden)

	// Оператор отклоняет обращение из new — до всякой работы эксперта.
	check(opTok, "POST", "/api/appeals/"+other+"/reject",
		map[string]string{"reason": "обращение вне компетенции"}, http.StatusOK)
}

// ТЗ 4.7: перебор трек-номеров ограничен (5/мин с задержкой).
func TestIT_TrackBruteforceRateLimit(t *testing.T) {
	e := newIT(t)
	bad := map[string]string{"track_number": "ОШИБКА-0001"}
	for i := 1; i <= 5; i++ {
		w := e.do(e.pub, "POST", "/api/appeals/track", "", nil, bad)
		if w.Code != http.StatusNotFound {
			t.Fatalf("попытка %d: код = %d, want 404", i, w.Code)
		}
	}
	w := e.do(e.pub, "POST", "/api/appeals/track", "", nil, bad)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("шестая попытка: код = %d, want 429", w.Code)
	}
}

// Подбор пароля сотрудника: 10 неудач в минуту — блокировка.
func TestIT_LoginBruteforceRateLimit(t *testing.T) {
	e := newIT(t)
	bad := map[string]string{"login": "admin", "password": "wrong-password"}
	for i := 1; i <= 10; i++ {
		w := e.do(e.staff, "POST", "/api/auth/login", "", nil, bad)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("попытка %d: код = %d, want 401", i, w.Code)
		}
	}
	w := e.do(e.staff, "POST", "/api/auth/login", "", nil, bad)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("11-я попытка: код = %d, want 429", w.Code)
	}
}

// ТЗ 5.x: терминальный статус — архив; админ не может ни назначить
// терминальный статус, ни «разблокировать» уже завершённое обращение.
func TestIT_AdminCannotChangeTerminalAppeal(t *testing.T) {
	e := newIT(t)
	appealID, _ := e.createAppeal("Обращение, которое оператор отклонит", "")
	opTok := e.login("operator")
	admTok := e.login("admin")

	// Оператор отклоняет обращение — оно уходит в архив.
	e.mustDo(e.staff, "POST", "/api/appeals/"+appealID+"/reject", opTok, nil,
		map[string]string{"reason": "обращение вне компетенции"}, http.StatusOK)

	// Админ не может вывести архивное обращение обратно в работу.
	e.mustDo(e.staff, "POST", "/api/appeals/"+appealID+"/admin-status", admTok, nil,
		map[string]string{"status": "new", "reason": "попытка разблокировки архива"},
		http.StatusBadRequest)

	// На живом (незавершённом) обращении разблокировка по-прежнему работает.
	liveID, _ := e.createAppeal("Живое обращение для разблокировки", "")
	e.mustDo(e.staff, "POST", "/api/appeals/"+liveID+"/admin-status", admTok, nil,
		map[string]string{"status": "assigned", "reason": "разблокировка зависшего"},
		http.StatusOK)
}

// CSV-экспорт: BOM для Excel, фиксированный заголовок, строка на обращение.
func TestIT_ExportCSV(t *testing.T) {
	e := newIT(t)
	appealID, _ := e.createAppeal("Обращение для проверки экспорта в CSV", "")
	admTok := e.login("admin")

	w := e.mustDo(e.staff, "GET", "/api/export/appeals", admTok, nil, nil, http.StatusOK)
	body := w.Body.String()
	if !strings.HasPrefix(body, "\ufeffid,applicant_type,category,status,priority,crisis,assigned_expert,returns,created_at,updated_at\r\n") {
		t.Errorf("неожиданный заголовок CSV: %q", body[:itMin(120, len(body))])
	}
	if !strings.Contains(body, appealID) {
		t.Error("в экспорте нет созданного обращения")
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q", ct)
	}

	// Эксперт получает только свои обращения.
	expTok := e.login("lawyer1")
	w = e.mustDo(e.staff, "GET", "/api/export/appeals", expTok, nil, nil, http.StatusOK)
	if strings.Contains(w.Body.String(), appealID) {
		t.Error("эксперту выгружено чужое обращение")
	}

	// Оператор получает только обращения, с которыми работал сам
	// (назначал специалиста или отклонял), а не весь массив программы.
	opTok := e.login("operator")
	w = e.mustDo(e.staff, "GET", "/api/export/appeals", opTok, nil, nil, http.StatusOK)
	if strings.Contains(w.Body.String(), appealID) {
		t.Error("оператору выгружено обращение, с которым он не работал")
	}

	// После назначения специалиста этим оператором обращение попадает в его выгрузку.
	e.mustDo(e.staff, "POST", "/api/appeals/"+appealID+"/assign", opTok, nil,
		map[string]string{"expert_id": e.expertID("psychologist1")}, http.StatusOK)
	w = e.mustDo(e.staff, "GET", "/api/export/appeals", opTok, nil, nil, http.StatusOK)
	if !strings.Contains(w.Body.String(), appealID) {
		t.Error("после назначения обращение не попало в выгрузку оператора")
	}
}

// TestIT_ExportCSVFilters — фильтры выгрузки из личного кабинета уточняют
// выборку по статусу, а некорректные значения отклоняются с 400.
func TestIT_ExportCSVFilters(t *testing.T) {
	e := newIT(t)
	idNew, _ := e.createAppeal("Обращение без обработчиков для фильтра экспорта", "")
	idReady, _ := e.createAppeal("Обращение с готовым ответом для фильтра экспорта", "")
	e.toAnswerReady(idReady) // доводит до answer_ready (assign → take → recommendation)
	admTok := e.login("admin")

	csv := func(query string) string {
		w := e.mustDo(e.staff, "GET", "/api/export/appeals"+query, admTok, nil, nil, http.StatusOK)
		return w.Body.String()
	}

	// Статусный фильтр: каждое обращение попадает только в свою выборку.
	if b := csv("?status=new"); !strings.Contains(b, idNew) || strings.Contains(b, idReady) {
		t.Error("фильтр status=new отдает неверный набор обращений")
	}
	if b := csv("?status=answer_ready"); !strings.Contains(b, idReady) || strings.Contains(b, idNew) {
		t.Error("фильтр status=answer_ready отдает неверный набор обращений")
	}

	// Приоритет: низкий существует только если проставлен вручную —
	// проверяем, что пересечение фильтров не роняет выборку.
	if b := csv("?status=new&priority=normal"); !strings.Contains(b, idNew) {
		t.Error("пересечение фильтров status+priority потеряло обращение с обычным приоритетом")
	}
	if b := csv("?status=new&priority=urgent"); strings.Contains(b, idNew) {
		t.Error("фильтр priority=urgent вернул обращение с обычным приоритетом")
	}

	// Некорректные значения — 400 с понятной ошибкой, без выгрузки.
	e.mustDo(e.staff, "GET", "/api/export/appeals?status=bogus", admTok, nil, nil, http.StatusBadRequest)
	e.mustDo(e.staff, "GET", "/api/export/appeals?group=unknown", admTok, nil, nil, http.StatusBadRequest)
	e.mustDo(e.staff, "GET", "/api/export/appeals?crisis=maybe", admTok, nil, nil, http.StatusBadRequest)

	// Ролевой охват сохраняется и под фильтром: специалист не получает чужое.
	expTok := e.login("lawyer1")
	w := e.mustDo(e.staff, "GET", "/api/export/appeals?status=new", expTok, nil, nil, http.StatusOK)
	if strings.Contains(w.Body.String(), idNew) {
		t.Error("специалисту под фильтром выгружено чужое обращение")
	}
}

// TestIT_AdminDashboard — дашборд отдаёт дневную серию для графика,
// лимит нагрузки и фильтруемый список обращений.
func TestIT_AdminDashboard(t *testing.T) {
	e := newIT(t)
	idNew, _ := e.createAppeal("Новое обращение для дашборда", "")
	idReady, _ := e.createAppeal("Доведённое обращение для дашборда", "")
	e.toAnswerReady(idReady)
	admTok := e.login("admin")

	// Статистика за сегодня: серия содержит сегодняшний день с созданными.
	today := time.Now().Format("2006-01-02")
	w := e.mustDo(e.staff, "GET", "/api/admin/stats?from="+today, admTok, nil, nil, http.StatusOK)
	var st struct {
		ByDay []struct {
			Day       string `json:"day"`
			Created   int    `json:"created"`
			Completed int    `json:"completed"`
		} `json:"by_day"`
		ExpertLimit int `json:"expert_limit"`
		Active      int `json:"active"`
		Total       int `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("не удалось разобрать статистику: %v %s", err, w.Body.String())
	}
	if len(st.ByDay) != 1 || st.ByDay[0].Day != today {
		t.Fatalf("by_day = %+v, want единственный день %s", st.ByDay, today)
	}
	if st.ByDay[0].Created < 2 {
		t.Errorf("созданных за сегодня = %d, want >= 2", st.ByDay[0].Created)
	}
	if st.ExpertLimit <= 0 {
		t.Errorf("expert_limit = %d, want > 0", st.ExpertLimit)
	}
	if st.Total < 2 || st.Active < 1 {
		t.Errorf("total=%d active=%d — метрики дашборда пусты", st.Total, st.Active)
	}

	// Список «все обращения» фильтруется по статусу: в выдаче только
	// запрошенный статус, и нужное обращение присутствует.
	// (БД общая на прогон, поэтому проверяем состав, а не количество.)
	var page struct {
		Appeals []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"appeals"`
	}
	w = e.mustDo(e.staff, "GET", "/api/admin/appeals?status=answer_ready", admTok, nil, nil, http.StatusOK)
	json.Unmarshal(w.Body.Bytes(), &page)
	found := false
	for _, a := range page.Appeals {
		if a.Status != "answer_ready" {
			t.Fatalf("фильтр answer_ready пропустил обращение в статусе %s", a.Status)
		}
		if a.ID == idReady {
			found = true
		}
	}
	if !found {
		t.Errorf("status=answer_ready не отдал обращение %s", idReady)
	}
	w = e.mustDo(e.staff, "GET", "/api/admin/appeals?status=bogus", admTok, nil, nil, http.StatusBadRequest)

	w = e.mustDo(e.staff, "GET", "/api/admin/appeals?status=new", admTok, nil, nil, http.StatusOK)
	json.Unmarshal(w.Body.Bytes(), &page)
	found = false
	for _, a := range page.Appeals {
		if a.Status != "new" {
			t.Fatalf("фильтр new пропустил обращение в статусе %s", a.Status)
		}
		if a.ID == idNew {
			found = true
		}
	}
	if !found {
		t.Errorf("status=new не отдал обращение %s", idNew)
	}
}

func itMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func testImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 16), G: uint8(y * 16), B: 128, A: 255})
		}
	}
	return img
}

// jpegWithExif собирает валидный JPEG с APP1 EXIF-сегментом сразу после SOI —
// так реальный фотофайл несёт метаданные (включая GPS).
func jpegWithExif(t *testing.T) []byte {
	t.Helper()
	var orig bytes.Buffer
	if err := jpeg.Encode(&orig, testImage(), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	payload := append([]byte("Exif\x00\x00"), []byte("MM\x00\x2aFAKE-GPS-DATA")...)
	segLen := len(payload) + 2 // длина включает само поле длины
	seg := append([]byte{0xFF, 0xE1, byte(segLen >> 8), byte(segLen)}, payload...)
	src := orig.Bytes()
	out := make([]byte, 0, len(src)+len(seg))
	out = append(out, src[:2]...) // SOI
	out = append(out, seg...)
	out = append(out, src[2:]...)
	return out
}

// Вложения: заявитель загружает JPEG с EXIF — в хранилище уходит без метаданных;
// файл доступен заявителю и сотрудникам, чужому заявителю — нет.
func TestIT_AttachmentsE2E(t *testing.T) {
	e := newIT(t)
	appealID, track := e.createAppeal("Прикладываю скриншот переписки из чата класса", "")
	ac := e.applicantCookie(track)

	jpegBytes := jpegWithExif(t)
	upload := func(h http.Handler, path, staffTok string, cookie *http.Cookie, content []byte, filename, contentType string) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("file", filename)
		_, _ = fw.Write(content)
		_ = mw.Close()
		r := httptest.NewRequest("POST", path, &buf)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		if staffTok != "" {
			r.AddCookie(&http.Cookie{Name: staffCookieName, Value: staffTok})
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	w := upload(e.pub, "/api/appeals/me/attachments", "", ac, jpegBytes, "photo.jpg", "image/jpeg")
	if w.Code != http.StatusCreated {
		t.Fatalf("загрузка вложения: код = %d, тело: %s", w.Code, w.Body.String())
	}
	var att struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &att); err != nil {
		t.Fatal(err)
	}

	// Запрещённый тип отсекается детектором содержимого.
	exe := []byte{'M', 'Z', 0x00, 0x01, 0x02, 0x00, 0x00, 0x00, 0xFF, 0x00, 0x0A, 0x00}
	w = upload(e.pub, "/api/appeals/me/attachments", "", ac, exe, "virus.exe", "application/octet-stream")
	if w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("загрузка exe: код = %d, want 415", w.Code)
	}

	// Заявитель скачивает: файл декодируется, EXIF удалён.
	w = e.mustDo(e.pub, "GET", "/api/appeals/me/attachments/"+att.ID, "", ac, nil, http.StatusOK)
	if got := w.Body.Bytes(); bytes.Contains(got, []byte("Exif\x00\x00")) {
		t.Error("EXIF выжил при скачивании заявителем")
	} else if _, format, err := image.Decode(bytes.NewReader(got)); err != nil || format != "jpeg" {
		t.Errorf("скачанный файл не декодируется как JPEG: %v %s", err, format)
	}

	// Оператор получает тот же файл по служебному пути.
	opTok := e.login("operator")
	e.mustDo(e.staff, "GET", "/api/appeals/"+appealID+"/attachments/"+att.ID, opTok, nil, nil, http.StatusOK)

	// Чужой заявитель не может скачать вложение.
	_, otherTrack := e.createAppeal("Другое обращение — проверка изоляции вложений", "")
	other := e.applicantCookie(otherTrack)
	w = e.do(e.pub, "GET", "/api/appeals/me/attachments/"+att.ID, "", other, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("чужой заявитель скачал вложение: код = %d, want 404", w.Code)
	}
}

// ТЗ 5.1: janitor закрывает обращения без ответа заявителя старше no_response_days.
func TestIT_JanitorAutoClose(t *testing.T) {
	e := newIT(t)
	appealID, _ := e.createAppeal("Обращение, на которое заявитель так и не вернётся", "")
	e.toAnswerReady(appealID) // автозакрытие работает из needs_clarification/answer_ready

	// Сдвигаем создание в прошлое дальше порога no_response_days (14 по умолчанию).
	ctx := context.Background()
	if _, err := e.st.DB.ExecContext(ctx,
		`UPDATE appeals SET created_at = now() - interval '30 days' WHERE id = $1`, appealID); err != nil {
		t.Fatal(err)
	}

	res, err := e.st.AutoCloseNoResponse(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Closed) < 1 {
		t.Fatalf("janitor закрыл %d обращений, want >= 1", len(res.Closed))
	}

	// В одном прогоне повторный запуск не закрывает уже терминальные обращения.
	if _, err := e.st.AutoCloseNoResponse(ctx); err != nil {
		t.Fatal(err)
	}

	w := e.do(e.staff, "GET", "/api/appeals/"+appealID+"/", e.login("operator"), nil, nil)
	if got := appealStatus(t, w); got != "closed_no_response" {
		t.Fatalf("статус после janitor = %q, want closed_no_response", got)
	}
}

// Присутствие: heartbeat оператора и эксперта на карточке обращения;
// каждый видит других, но не себя; чужой эксперт не проходит проверку доступа.
func TestIT_Presence(t *testing.T) {
	e := newIT(t)
	appealID, _ := e.createAppeal("Проверка индикатора присутствия на карточке", "")
	opTok, expTok := e.toAnswerReady(appealID)

	e.mustDo(e.staff, "POST", "/api/appeals/"+appealID+"/presence", opTok, nil,
		map[string]bool{"typing": false}, http.StatusOK)
	e.mustDo(e.staff, "POST", "/api/appeals/"+appealID+"/presence", expTok, nil,
		map[string]bool{"typing": true}, http.StatusOK)

	// Оператор видит только эксперта — с флагом «печатает».
	w := e.mustDo(e.staff, "GET", "/api/appeals/"+appealID+"/presence", opTok, nil, nil, http.StatusOK)
	if body := w.Body.String(); !strings.Contains(body, `"login":"psychologist1"`) || !strings.Contains(body, `"typing":true`) {
		t.Errorf("оператор должен видеть эксперта с typing=true: %s", body)
	}
	if strings.Contains(w.Body.String(), `"login":"operator"`) {
		t.Errorf("участник не должен видеть сам себя: %s", w.Body.String())
	}

	// Эксперт видит оператора без флага «печатает».
	w = e.mustDo(e.staff, "GET", "/api/appeals/"+appealID+"/presence", expTok, nil, nil, http.StatusOK)
	if body := w.Body.String(); !strings.Contains(body, `"login":"operator"`) || strings.Contains(body, `"typing":true`) {
		t.Errorf("эксперт должен видеть оператора без typing: %s", body)
	}

	// Чужой эксперт не допускается к присутствию на обращении.
	w = e.do(e.staff, "POST", "/api/appeals/"+appealID+"/presence", e.login("lawyer1"), nil,
		map[string]bool{"typing": true})
	if w.Code != http.StatusForbidden {
		t.Errorf("посторонний эксперт в присутствии: код = %d, want 403", w.Code)
	}
}

// Смена пароля сотрудником: /api/auth/password проверяет текущий пароль,
// обновляет хеш и инвалидирует все сессии, кроме той, из которой меняли.
func TestIT_ChangePassword(t *testing.T) {
	e := newIT(t)
	admTok := e.login("admin")

	// Отдельный пользователь, чтобы не ломать демо-учётки для остальных тестов.
	e.mustDo(e.staff, "POST", "/api/admin/users", admTok, nil, map[string]string{
		"login": "tmpuser1", "password": "temp-pass-123", "role": "operator",
	}, http.StatusCreated)

	loginAs := func(login, pwd string) string {
		e.t.Helper()
		w := e.mustDo(e.staff, "POST", "/api/auth/login", "", nil,
			map[string]string{"login": login, "password": pwd}, http.StatusOK)
		return e.staffTokenOf(w)
	}

	// Без аутентификации эндпоинт закрыт.
	e.mustDo(e.staff, "POST", "/api/auth/password", "", nil,
		map[string]string{"current_password": "x", "new_password": "y"}, http.StatusUnauthorized)

	tok1 := loginAs("tmpuser1", "temp-pass-123")
	tok2 := loginAs("tmpuser1", "temp-pass-123")

	// Неверный текущий пароль отклоняется.
	e.mustDo(e.staff, "POST", "/api/auth/password", tok1, nil,
		map[string]string{"current_password": "wrong", "new_password": "new-pass-456"}, http.StatusUnauthorized)
	// Слишком короткий и совпадающий с текущим — отклоняются.
	e.mustDo(e.staff, "POST", "/api/auth/password", tok1, nil,
		map[string]string{"current_password": "temp-pass-123", "new_password": "short"}, http.StatusBadRequest)
	e.mustDo(e.staff, "POST", "/api/auth/password", tok1, nil,
		map[string]string{"current_password": "temp-pass-123", "new_password": "temp-pass-123"}, http.StatusBadRequest)

	// Корректная смена.
	e.mustDo(e.staff, "POST", "/api/auth/password", tok1, nil,
		map[string]string{"current_password": "temp-pass-123", "new_password": "new-pass-456"}, http.StatusOK)

	// Текущая сессия жива, вторая — инвалидирована.
	e.mustDo(e.staff, "GET", "/api/mystats", tok1, nil, nil, http.StatusOK)
	e.mustDo(e.staff, "GET", "/api/mystats", tok2, nil, nil, http.StatusUnauthorized)

	// Старый пароль больше не работает, новый — входит.
	e.mustDo(e.staff, "POST", "/api/auth/login", "", nil,
		map[string]string{"login": "tmpuser1", "password": "temp-pass-123"}, http.StatusUnauthorized)
	loginAs("tmpuser1", "new-pass-456")
}

// Заметки — служебный инструмент эксперта: оператор не читает их даже по API
// (раньше запрет был только на клиенте), эксперт видит свои заметки.
func TestIT_NotesExpertOnly(t *testing.T) {
	e := newIT(t)
	appealID, _ := e.createAppeal("Просьба помочь с конфликтом в классе", "")
	opTok, expTok := e.toAnswerReady(appealID)

	// Эксперт оставляет заметку.
	e.mustDo(e.staff, "POST", "/api/appeals/"+appealID+"/notes", expTok, nil,
		map[string]string{"text": "Связаться с классным руководителем"}, http.StatusCreated)

	// Оператор не читает и не пишет заметки.
	w := e.do(e.staff, "GET", "/api/appeals/"+appealID+"/notes", opTok, nil, nil)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "expert only") {
		t.Errorf("оператор не должен читать заметки: %d %s", w.Code, w.Body.String())
	}
	w = e.do(e.staff, "POST", "/api/appeals/"+appealID+"/notes", opTok, nil,
		map[string]string{"text": "служебная заметка оператора"})
	if w.Code != http.StatusForbidden {
		t.Errorf("оператор не должен писать заметки: %d %s", w.Code, w.Body.String())
	}

	// Эксперт читает свои заметки.
	w = e.mustDo(e.staff, "GET", "/api/appeals/"+appealID+"/notes", expTok, nil, nil, http.StatusOK)
	if !strings.Contains(w.Body.String(), "классным руководителем") {
		t.Errorf("эксперт должен видеть свои заметки: %s", w.Body.String())
	}
}

// Джанистор раз в час подчищает истёкшие сессии обеих таблиц,
// не задевая живые.
func TestIT_PurgeExpiredSessions(t *testing.T) {
	e := newIT(t)
	ctx := context.Background()

	// Живые сессии: сотрудник и заявитель.
	opTok := e.login("operator")
	appealID, track := e.createAppeal("Обращение с живой сессией заявителя", "")
	appCookie := e.applicantCookie(track)

	// Истёкшие строки — вставляем напрямую.
	if _, err := e.st.DB.ExecContext(ctx,
		`INSERT INTO staff_sessions (token_hash, user_id, expires_at)
		 VALUES ('expired-staff', (SELECT id FROM users WHERE login = 'operator'), now() - interval '1 minute')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.DB.ExecContext(ctx,
		`INSERT INTO applicant_sessions (token_hash, appeal_id, expires_at)
		 VALUES ('expired-applicant', $1, now() - interval '1 minute')`, appealID); err != nil {
		t.Fatal(err)
	}

	n, err := e.st.PurgeExpiredSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n < 2 {
		t.Errorf("удалено истёкших сессий = %d, want >= 2", n)
	}

	// Истёкшие строки исчезли, живые — на месте.
	for _, q := range []string{
		`SELECT count(*) FROM staff_sessions WHERE token_hash = 'expired-staff'`,
		`SELECT count(*) FROM applicant_sessions WHERE token_hash = 'expired-applicant'`,
	} {
		var c int
		if err := e.st.DB.QueryRowContext(ctx, q).Scan(&c); err != nil || c != 0 {
			t.Errorf("истёкшая сессия не удалена: count=%d err=%v", c, err)
		}
	}

	// Живая сессия сотрудника продолжает работать.
	e.mustDo(e.staff, "GET", "/api/operator/queue", opTok, nil, nil, http.StatusOK)
	// И живая сессия заявителя.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/appeals/me/", nil)
	r.AddCookie(appCookie)
	e.pub.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("живая сессия заявителя повреждена: %d %s", w.Code, w.Body.String())
	}
}

// Личный кабинет сотрудника: /api/profile собирает карточку профиля,
// аналитику (оператор/эксперт), нагрузку (эксперт) и журнал последних действий.
func TestIT_Profile(t *testing.T) {
	e := newIT(t)

	// Без сессии эндпоинт закрыт.
	e.mustDo(e.staff, "GET", "/api/profile", "", nil, nil, http.StatusUnauthorized)

	// Эксперт, у которого есть действия по обращению.
	appealID, _ := e.createAppeal("Профиль: тестовое обращение", "")
	opTok, expTok := e.toAnswerReady(appealID)

	w := e.mustDo(e.staff, "GET", "/api/profile", expTok, nil, nil, http.StatusOK)
	body := w.Body.String()
	for _, want := range []string{`"role":"expert"`, `"workload"`, `"stats"`, `"recent_events"`, `"event_type":"status"`} {
		if !strings.Contains(body, want) {
			t.Errorf("профиль эксперта: нет %s: %s", want, body)
		}
	}

	// Оператору нагрузка не нужна, аналитика — есть.
	w = e.mustDo(e.staff, "GET", "/api/profile", opTok, nil, nil, http.StatusOK)
	if body := w.Body.String(); strings.Contains(body, `"workload"`) {
		t.Errorf("профиль оператора не должен содержать workload: %s", body)
	} else if !strings.Contains(body, `"role":"operator"`) || !strings.Contains(body, `"stats"`) {
		t.Errorf("профиль оператора неполный: %s", body)
	}

	// Администратору — карточка и журнал, без аналитики и нагрузки.
	adminTok := e.login("admin")
	w = e.mustDo(e.staff, "GET", "/api/profile", adminTok, nil, nil, http.StatusOK)
	if body := w.Body.String(); strings.Contains(body, `"workload"`) || strings.Contains(body, `"stats"`) {
		t.Errorf("профиль админа не должен содержать workload/stats: %s", body)
	} else if !strings.Contains(body, `"role":"admin"`) || !strings.Contains(body, `"recent_events"`) {
		t.Errorf("профиль админа неполный: %s", body)
	}

	// Страница кабинета отдаётся.
	w = e.mustDo(e.staff, "GET", "/profile", expTok, nil, nil, http.StatusOK)
	if !strings.Contains(w.Body.String(), "Личный кабинет") {
		t.Errorf("страница /profile должна содержать заголовок: %s", w.Body.String()[:200])
	}
}
