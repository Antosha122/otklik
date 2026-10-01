// Package totp — генерация и проверка одноразовых кодов RFC 6238 (TOTP,
// HMAC-SHA1, 6 цифр, шаг 30 с) без внешних зависимостей: нужен только stdlib.
// Второй фактор входа для сотрудников (см. internal/httpapi/totp.go).
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	step     = 30 * time.Second
	digits   = 6
	secretLn = 20 // 160 бит — стандартный размер ключа Google Authenticator
)

// b32 — base32 без паддинга: так секрет выглядят во всех приложениях-аутентификаторах.
var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret возвращает случайный секрет в base32 (20 байт энтропии).
func GenerateSecret() (string, error) {
	buf := make([]byte, secretLn)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return b32.EncodeToString(buf), nil
}

// hotp считает код RFC 4226 для конкретного счётчика.
func hotp(key []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", digits, n%1_000_000)
}

func decodeSecret(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.TrimSpace(secret))
	s = strings.TrimRight(s, "=")
	return b32.DecodeString(s)
}

// Verify проверяет 6-значный код с допуском ±1 шаг (30 с) — часы клиента
// могут чуть уходить. Сравнение постоянное по времени. Пустой/кривой секрет
// или код неверной длины — просто false, без паники.
func Verify(secret, code string, now time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != digits {
		return false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false
		}
	}
	key, err := decodeSecret(secret)
	if err != nil {
		return false
	}
	counter := uint64(now.Unix()) / uint64(step.Seconds())
	for _, drift := range [3]int64{-1, 0, 1} {
		want := hotp(key, uint64(int64(counter)+drift))
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

// Code возвращает текущий код (для тестов и отладки; фронту не отдаём).
func Code(secret string, now time.Time) (string, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}
	return hotp(key, uint64(now.Unix())/uint64(step.Seconds())), nil
}

// OTPAuthURL строит otpauth://totp-ссылку, которую понимают мобильные
// приложения-аутентификаторы (Google Authenticator, Aegis, FreeOTP...).
func OTPAuthURL(secret, account, issuer string) string {
	u := url.URL{
		Scheme: "otpauth",
		Host:   "totp",
		Path:   url.PathEscape(issuer) + ":" + url.PathEscape(account),
	}
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	u.RawQuery = q.Encode()
	return u.String()
}
