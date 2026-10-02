package domain

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// trackAlphabet — алфавит без похожих символов (0, O, 1, I, l).
const trackAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

func GenerateTrackNumber() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	chars := make([]byte, 12)
	for i, b := range buf {
		chars[i] = trackAlphabet[int(b)%len(trackAlphabet)]
	}
	return fmt.Sprintf("ОТК-%s-%s-%s", string(chars[0:4]), string(chars[4:8]), string(chars[8:12])), nil
}

func NormalizeTrack(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToUpper(s)
	s = strings.ReplaceAll(s, "Ё", "Е")
	s = strings.ReplaceAll(s, "OTK", "ОТК")
	parts := strings.Split(s, "-")
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p)
	}
	return b.String()
}

// HashTrack — легаси-режим (несолёный SHA-256): так хешировались номера до
// появления TRACK_HMAC_KEY, поэтому функция нужна для проверки старых обращений.
func HashTrack(number string) string {
	h := sha256.Sum256([]byte(NormalizeTrack(number)))
	return hex.EncodeToString(h[:])
}

// HashTrackWithKey считает хеш трек-номера для хранения в БД. С ключом —
// HMAC-SHA256: при утечке базы офлайн-перебор номеров на GPU невозможен без
// секрета сервера. Пустой ключ — совместимость (обычный SHA-256).
func HashTrackWithKey(number, key string) string {
	if key == "" {
		return HashTrack(number)
	}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(NormalizeTrack(number)))
	return hex.EncodeToString(mac.Sum(nil))
}
