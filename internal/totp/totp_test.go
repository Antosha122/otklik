package totp

import (
	"strings"
	"testing"
	"time"
)

// Векторы RFC 4226 (Appendix D) / RFC 6238: секрет «12345678901234567890»
// в base32, HMAC-SHA1. 6-значный код = тот же HOTP mod 10^6.
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestVerify_RFCVectors(t *testing.T) {
	cases := []struct {
		unix int64
		code string
	}{
		{59, "287082"},          // counter 1, RFC 4226
		{1111111109, "081804"},  // RFC 6238, SHA1
	}
	for _, c := range cases {
		if !Verify(rfcSecret, c.code, time.Unix(c.unix, 0).UTC()) {
			t.Errorf("T=%d: код %s должен подходить", c.unix, c.code)
		}
	}
}

func TestVerify_Window(t *testing.T) {
	now := time.Unix(59, 0).UTC()
	// Соседние шаги (±30 с) принимаются: часы клиента могут уходить.
	for _, drift := range []time.Duration{-30 * time.Second, 30 * time.Second} {
		code, err := Code(rfcSecret, now.Add(drift))
		if err != nil {
			t.Fatal(err)
		}
		if !Verify(rfcSecret, code, now) {
			t.Errorf("код соседнего шага (drift %v) должен подходить", drift)
		}
	}
	// Через два шага — уже нет.
	code, _ := Code(rfcSecret, now.Add(90*time.Second))
	if Verify(rfcSecret, code, now) {
		t.Error("код через два шага не должен подходить")
	}
}

func TestVerify_Rejects(t *testing.T) {
	now := time.Unix(59, 0).UTC()
	for _, bad := range []string{"", "12345", "1234567", "abcdef", "  28708 2", "000000"} {
		if bad == "287082" {
			continue
		}
		if Verify(rfcSecret, bad, now) {
			t.Errorf("код %q не должен подходить", bad)
		}
	}
	if Verify("not-base32!!", "287082", now) {
		t.Error("кривой секрет не должен давать верных кодов")
	}
	// Пробелы вокруг кода — норм (скопировали из приложения).
	if !Verify(rfcSecret, " 287082 ", now) {
		t.Error("код с пробелами по краям должен подходить")
	}
}

func TestGenerateSecret(t *testing.T) {
	s1, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := GenerateSecret()
	// 20 байт → 32 base32-символа без паддинга.
	if len(s1) != 32 || strings.ContainsAny(s1, "= ") {
		t.Errorf("секрет %q: ждём 32 base32-символа без паддинга", s1)
	}
	if s1 == s2 {
		t.Error("секреты должны быть разными")
	}
	if _, err := Code(s1, time.Now()); err != nil {
		t.Errorf("сгенерированный секрет должен декодироваться: %v", err)
	}
}

func TestOTPAuthURL(t *testing.T) {
	u := OTPAuthURL("ABC234DEF", "admin", "Otklik")
	for _, want := range []string{"otpauth://totp/", "secret=ABC234DEF", "issuer=Otklik", "digits=6", "period=30"} {
		if !strings.Contains(u, want) {
			t.Errorf("otpauth-url %q: нет %q", u, want)
		}
	}
}
