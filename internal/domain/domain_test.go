package domain_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"otklik/internal/domain"
)

func everyStatus() []domain.Status {
	return []domain.Status{
		domain.StatusNew, domain.StatusAssigned, domain.StatusInProgress, domain.StatusNeedsClarification,
		domain.StatusAnswerReady, domain.StatusCompleted, domain.StatusReturned, domain.StatusRejected, domain.StatusClosedNoResponse,
	}
}

// ТЗ 4.4: каждый статус объясняется заявителю просто, в двух тональностях.
func TestExplanationForEveryStatus(t *testing.T) {
	for _, s := range everyStatus() {
		informal := s.ExplanationFor(domain.ApplicantSchoolchild)
		formal := s.ExplanationFor(domain.ApplicantParent)
		if strings.TrimSpace(informal) == "" {
			t.Errorf("%s: пустое объяснение на «ты»", s)
		}
		if strings.TrimSpace(formal) == "" {
			t.Errorf("%s: пустое объяснение на «вы»", s)
		}
		if informal == formal {
			t.Errorf("%s: тональности не должны совпадать", s)
		}
	}
}

// ТЗ 5.1: при отказе заявитель получает альтернативу — телефон доверия.
func TestExplanationRejectedHasHelpline(t *testing.T) {
	for _, at := range []domain.ApplicantType{domain.ApplicantSchoolchild, domain.ApplicantParent, domain.ApplicantTeacher} {
		if !strings.Contains(domain.StatusRejected.ExplanationFor(at), "8-800-2000-122") {
			t.Errorf("отказ для %s должен содержать телефон доверия", at)
		}
	}
}

func TestExplanationUnknownStatusEmpty(t *testing.T) {
	if got := domain.Status("hacked").ExplanationFor(domain.ApplicantSchoolchild); got != "" {
		t.Errorf("неизвестный статус должен давать пустое объяснение, got %q", got)
	}
}

func TestStatusValid(t *testing.T) {
	for _, s := range everyStatus() {
		if !s.Valid() {
			t.Errorf("Status %s should be valid", s)
		}
	}
	for _, s := range []domain.Status{"", "unknown", "NEW"} {
		if s.Valid() {
			t.Errorf("Status %q should be invalid", s)
		}
	}
}

func TestPriorityValid(t *testing.T) {
	for _, p := range []domain.Priority{domain.PriorityLow, domain.PriorityNormal, domain.PriorityUrgent} {
		if !p.Valid() {
			t.Errorf("Priority %s should be valid", p)
		}
	}
	for _, p := range []domain.Priority{"", "high", "URGENT"} {
		if p.Valid() {
			t.Errorf("Priority %q should be invalid", p)
		}
	}
}

func TestApplicantTypeValid(t *testing.T) {
	for _, at := range []domain.ApplicantType{domain.ApplicantSchoolchild, domain.ApplicantParent, domain.ApplicantTeacher} {
		if !at.Valid() {
			t.Errorf("ApplicantType %s should be valid", at)
		}
	}
	for _, at := range []domain.ApplicantType{"", "alien", "PARENT"} {
		if at.Valid() {
			t.Errorf("ApplicantType %q should be invalid", at)
		}
	}
}

func TestPrincipalIsStaff(t *testing.T) {
	staff := []domain.Role{domain.RoleOperator, domain.RoleExpert, domain.RoleAdmin}
	for _, r := range staff {
		if !(domain.Principal{Role: r}).IsStaff() {
			t.Errorf("Role %s should be staff", r)
		}
	}
	if (domain.Principal{Role: domain.RoleApplicant, AppealID: uuid.New()}).IsStaff() {
		t.Error("applicant should not be staff")
	}
}

// ТЗ: 4 необязательных уточняющих вопроса с вариантами ответа.
func TestIntakeQuestionsPerSpec(t *testing.T) {
	if len(domain.IntakeQuestions) != 4 {
		t.Fatalf("expected 4 intake questions, got %d", len(domain.IntakeQuestions))
	}
	for _, q := range domain.IntakeQuestions {
		if strings.TrimSpace(q.Text) == "" {
			t.Errorf("пустой текст вопроса: %+v", q)
		}
		if len(q.Options) < 3 {
			t.Errorf("вопрос %q должен иметь минимум 3 варианта, got %d", q.Text, len(q.Options))
		}
		for _, o := range q.Options {
			if strings.TrimSpace(o) == "" {
				t.Errorf("пустой вариант в вопросе %q", q.Text)
			}
		}
	}
}

// ТЗ «Кризисные обращения»: справочник контактов должен быть заполнен
// и содержать детский телефон доверия.
func TestCrisisHelpContacts(t *testing.T) {
	if len(domain.CrisisHelpContacts) < 3 {
		t.Fatalf("expected at least 3 crisis contacts, got %d", len(domain.CrisisHelpContacts))
	}
	hasHelpline := false
	for _, c := range domain.CrisisHelpContacts {
		if c.Title == "" || c.Phone == "" || c.Description == "" {
			t.Errorf("неполный контакт: %+v", c)
		}
		if c.Phone == "8-800-2000-122" {
			hasHelpline = true
		}
	}
	if !hasHelpline {
		t.Error("справочник должен содержать детский телефон доверия 8-800-2000-122")
	}
}

// Нормализация кризисного текста: ё, регистр и переводы строк не мешают детекту.
func TestDetectCrisisNormalization(t *testing.T) {
	if !domain.DetectCrisis("  ВСЁ   НАДОЕЛО\nпо-настоящему ") {
		t.Error("DetectCrisis должен находить маркер при лишних пробелах и регистре")
	}
}

func TestCanTransition(t *testing.T) {
	allowed := []struct{ from, to domain.Status }{
		{domain.StatusNew, domain.StatusAssigned},
		{domain.StatusNew, domain.StatusRejected},
		{domain.StatusNew, domain.StatusCompleted},
		{domain.StatusAssigned, domain.StatusInProgress},
		{domain.StatusInProgress, domain.StatusNeedsClarification},
		{domain.StatusInProgress, domain.StatusAnswerReady},
		{domain.StatusNeedsClarification, domain.StatusInProgress},
		{domain.StatusNeedsClarification, domain.StatusClosedNoResponse},
		{domain.StatusAnswerReady, domain.StatusCompleted},
		{domain.StatusAnswerReady, domain.StatusReturned},
		{domain.StatusAnswerReady, domain.StatusClosedNoResponse},
		{domain.StatusReturned, domain.StatusAssigned},
		{domain.StatusReturned, domain.StatusRejected},
	}
	for _, tc := range allowed {
		if !domain.CanTransition(tc.from, tc.to) {
			t.Errorf("CanTransition(%s, %s) = false, want true", tc.from, tc.to)
		}
	}

	forbidden := []struct{ from, to domain.Status }{
		{domain.StatusNew, domain.StatusInProgress},       // минуя назначение
		{domain.StatusAssigned, domain.StatusAnswerReady}, // минуя работу
		{domain.StatusCompleted, domain.StatusNew},        // терминальный статус
		{domain.StatusRejected, domain.StatusAssigned},    // терминальный статус
		{domain.StatusClosedNoResponse, domain.StatusInProgress},
		{domain.StatusInProgress, domain.StatusInProgress}, // самопереход
		{domain.StatusNew, domain.Status("hacked")},        // неизвестный статус
	}
	for _, tc := range forbidden {
		if domain.CanTransition(tc.from, tc.to) {
			t.Errorf("CanTransition(%s, %s) = true, want false", tc.from, tc.to)
		}
	}
}

func TestTerminal(t *testing.T) {
	terminal := []domain.Status{domain.StatusCompleted, domain.StatusRejected, domain.StatusClosedNoResponse}
	for _, s := range terminal {
		if !s.Terminal() {
			t.Errorf("Status %s should be terminal", s)
		}
	}
	for _, s := range []domain.Status{domain.StatusNew, domain.StatusAssigned, domain.StatusInProgress, domain.StatusNeedsClarification, domain.StatusAnswerReady, domain.StatusReturned} {
		if s.Terminal() {
			t.Errorf("Status %s should not be terminal", s)
		}
	}
}

func TestDetectCrisis(t *testing.T) {
	positive := []string{
		"Мне не хочу жить",
		"ХОЧУ УМЕРЕТЬ", // регистр не важен
		"Он сказал, что убьёт меня",
		"Родители избивают",
		"всё надоело",
	}
	for _, s := range positive {
		if !domain.DetectCrisis(s) {
			t.Errorf("DetectCrisis(%q) = false, want true", s)
		}
	}
	if domain.DetectCrisis("Всё хорошо, просто спросить про расписание") {
		t.Error("DetectCrisis should be false for neutral text")
	}
	// Несколько текстов: маркер может быть в любом из них (описание + анкета).
	if !domain.DetectCrisis("всё нормально", "дома угрожают") {
		t.Error("DetectCrisis should scan all texts")
	}
}

// Расширенные смысловые группы словаря: каждая группа должна срабатывать.
func TestDetectCrisisExpandedMarkers(t *testing.T) {
	positive := []string{
		// Суицидальные намерения (конкретные способы и формулировки).
		"не хочется жить больше",
		"хочу выпрыгнуть из окна",
		"думаю повеситься",
		"выпью таблетки и не проснусь",
		"хочу свести счеты с жизнью",
		"лучше бы умер, чем так жить",
		// Самоповреждение.
		"я режу себя, когда плохо",
		// Насилие, угрозы.
		"дома бьют за оценки",
		"старшие угрожают ножом",
		"он приехал с пистолетом",
		// Сексуальное насилие.
		"сосед домогается ко мне",
	}
	for _, s := range positive {
		if !domain.DetectCrisis(s) {
			t.Errorf("DetectCrisis(%q) = false, want true", s)
		}
	}
	// Нейтральные школьные тексты не должны давать ложных срабатываний.
	neutral := []string{
		"Как записаться в кружок робототехники?",
		"Когда будет родительское собрание?",
		"Не понял тему по математике, объясните дроби",
		"потерял пропуск в столовую",
	}
	for _, s := range neutral {
		if domain.DetectCrisis(s) {
			t.Errorf("DetectCrisis(%q) = true, want false (ложное срабатывание)", s)
		}
	}
}

func TestNormalizeTrack(t *testing.T) {
	cases := map[string]string{
		"ОТК-X7KD-R9MF-Q3HP":   "ОТКX7KDR9MFQ3HP",
		"otk-x7kd-r9mf-q3hp":   "ОТКX7KDR9MFQ3HP", // латинская OTK -> ОТК
		" отк-X7KД-R9mf-q3hp ": "ОТКX7KДR9MFQ3HP", // пробелы и регистр
	}
	for in, want := range cases {
		if got := domain.NormalizeTrack(in); got != want {
			t.Errorf("NormalizeTrack(%q) = %q, want %q", in, got, want)
		}
	}
	// Один и тот же номер в разном написании даёт один хеш.
	if domain.HashTrack("ОТК-X7KD-R9MF-Q3HP") != domain.HashTrack("otk-x7kd-r9mf-q3hp") {
		t.Error("HashTrack should be stable across notations")
	}
}

func TestGenerateTrackNumber(t *testing.T) {
	re := regexp.MustCompile(`^ОТК-[23456789ABCDEFGHJKMNPQRSTUVWXYZ]{4}-[A-Z\d]{4}-[A-Z\d]{4}$`)
	for i := 0; i < 100; i++ {
		tn, err := domain.GenerateTrackNumber()
		if err != nil {
			t.Fatalf("GenerateTrackNumber: %v", err)
		}
		if !re.MatchString(tn) {
			t.Fatalf("track number %q does not match expected format", tn)
		}
	}
}
