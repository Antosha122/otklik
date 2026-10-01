package httpapi

import (
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestClientIPBehindTrustedProxy(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "172.18.0.4:51234" // Caddy в закрытой docker-сети (приватный адрес)
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := clientIP(r); got != "203.0.113.9" {
		t.Fatalf("clientIP = %q, want клиентский адрес из XFF", got)
	}
	// Клиент не контролирует последний элемент — его дописал прокси.
	r.Header.Set("X-Forwarded-For", "198.51.100.1, 203.0.113.9")
	if got := clientIP(r); got != "203.0.113.9" {
		t.Fatalf("clientIP = %q, want последний элемент XFF", got)
	}
}

func TestClientIPDirectIgnoresSpoofedXFF(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "203.0.113.9:40000" // «напрямую из интернета»
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := clientIP(r); got != "203.0.113.9" {
		t.Fatalf("clientIP = %q, want RemoteAddr: подделанный XFF должен игнорироваться", got)
	}
}

func TestClientIPGarbageXFF(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "10.0.0.2:3333"
	r.Header.Set("X-Forwarded-For", "not-an-ip")
	if got := clientIP(r); got != "10.0.0.2" {
		t.Fatalf("clientIP = %q, want RemoteAddr при мусорном XFF", got)
	}
}

func TestRateLimiterAllowAndSweep(t *testing.T) {
	rl := &rateLimiter{hits: map[string][]time.Time{}}
	for i := 0; i < 3; i++ {
		if !rl.allow("k", 3, time.Hour) {
			t.Fatalf("обращение %d не прошло лимит 3", i+1)
		}
	}
	if rl.allow("k", 3, time.Hour) {
		t.Fatal("4-е обращение прошло лимит 3")
	}
	rl.hits["k"] = []time.Time{time.Now().Add(-2 * time.Hour)} // всё остыло
	rl.sweep(time.Hour)
	rl.mu.Lock()
	_, exists := rl.hits["k"]
	rl.mu.Unlock()
	if exists {
		t.Fatal("sweep не удалил остывший ключ")
	}
}

func TestRateLimiterKeyCap(t *testing.T) {
	rl := &rateLimiter{hits: map[string][]time.Time{}}
	rl.mu.Lock()
	for i := 0; i < rlMaxKeys; i++ {
		rl.hits[strconv.Itoa(i)] = []time.Time{time.Now()}
	}
	rl.mu.Unlock()
	if rl.allow("new-key", 5, time.Hour) {
		t.Fatal("новый ключ завёлся при исчерпанном лимите ключей")
	}
}