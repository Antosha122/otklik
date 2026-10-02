package push

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestSender вЂ” РѕС‚РїСЂР°РІРёС‚РµР»СЊ СЃРѕ СЃРІРµР¶РµСЃРіРµРЅРµСЂРёСЂРѕРІР°РЅРЅРѕР№ РїР°СЂРѕР№ VAPID-РєР»СЋС‡РµР№.
func newTestSender(t *testing.T) *Sender {
	t.Helper()
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSender(pub, priv, "mailto:test@example.com")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestEncryptDecryptRoundTrip вЂ” РіР»Р°РІРЅС‹Р№ С‚РµСЃС‚ РєРѕСЂСЂРµРєС‚РЅРѕСЃС‚Рё RFC 8291: С€РёС„СЂСѓРµРј
// payload РґР»СЏ В«Р±СЂР°СѓР·РµСЂРЅРѕР№В» РїРѕРґРїРёСЃРєРё Рё СЂР°СЃС€РёС„СЂРѕРІС‹РІР°РµРј СЃР°РјРё, РїРѕРІС‚РѕСЂСЏСЏ РІС‹РІРѕРґС‹
// РєР»СЋС‡РµР№ РЅР° СЃС‚РѕСЂРѕРЅРµ РїРѕР»СѓС‡Р°С‚РµР»СЏ. Р•СЃР»Рё С„РѕСЂРјР°С‚ С‚РµР»Р° РёР»Рё HKDF-РїР°СЂР°РјРµС‚СЂС‹ РЅРµРІРµСЂРЅС‹,
// СЂРµР°Р»СЊРЅС‹Р№ push-СЃРµСЂРІРёСЃ СЂР°СЃС€РёС„СЂРѕРІР°С‚СЊ СЃРѕРѕР±С‰РµРЅРёРµ РЅРµ СЃРјРѕР¶РµС‚ вЂ” Рё СЌС‚РѕС‚ С‚РµСЃС‚ РїРѕР№РјР°РµС‚
// СЂРѕРІРЅРѕ СЌС‚Рѕ СЂР°СЃСЃРѕРіР»Р°СЃРѕРІР°РЅРёРµ.
func TestEncryptDecryptRoundTrip(t *testing.T) {
	s := newTestSender(t)

	// РЎС‚РѕСЂРѕРЅР° Р±СЂР°СѓР·РµСЂР°: СЃРІРѕСЏ РїР°СЂР° P-256 Рё auth-СЃРµРєСЂРµС‚.
	uaPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	sub := Subscription{
		Endpoint: "https://fcm.googleapis.com/fcm/send/test-token",
		P256DH:   b64.EncodeToString(uaPriv.PublicKey().Bytes()),
		Auth:     b64.EncodeToString(auth),
	}

	payload, err := json.Marshal(Message{Title: "РћС‚РєР»РёРє", Body: "РќРѕРІРѕРµ СЃРѕРѕР±С‰РµРЅРёРµ", URL: "/appeal"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := s.encryptFor(payload, sub)
	if err != nil {
		t.Fatal(err)
	}

	// Р Р°Р·Р±РѕСЂ С‚РµР»Р° aes128gcm: salt(16) | rs(4) | idlen(1) | server_pub | ct.
	if len(body) < 21+65+16 {
		t.Fatalf("С‚РµР»Рѕ СЃР»РёС€РєРѕРј РєРѕСЂРѕС‚РєРѕРµ: %d Р±Р°Р№С‚", len(body))
	}
	salt := body[:16]
	rs := binary.BigEndian.Uint32(body[16:20])
	if rs < 18 || rs > 4096 {
		t.Fatalf("rs = %d, РІРЅРµ РґРѕРїСѓСЃС‚РёРјРѕРіРѕ РґРёР°РїР°Р·РѕРЅР°", rs)
	}
	if idlen := int(body[20]); idlen != 65 {
		t.Fatalf("idlen = %d, want 65", idlen)
	}
	serverPubRaw := body[21 : 21+65]
	ct := body[21+65:]
	if serverPubRaw[0] != 4 {
		t.Fatalf("РєР»СЋС‡ СЃРµСЂРІРµСЂР° РЅРµ uncompressed: РїСЂРµС„РёРєСЃ %d", serverPubRaw[0])
	}

	// Р’С‹РІРѕРґ РєР»СЋС‡РµР№ РїРѕР»СѓС‡Р°С‚РµР»РµРј (RFC 8291, С‚Рµ Р¶Рµ С„РѕСЂРјСѓР»С‹).
	serverPub, err := ecdh.P256().NewPublicKey(serverPubRaw)
	if err != nil {
		t.Fatal(err)
	}
	ikm, err := uaPriv.ECDH(serverPub)
	if err != nil {
		t.Fatal(err)
	}
	info := append(append([]byte("WebPush: info\x00"), uaPriv.PublicKey().Bytes()...), serverPubRaw...)
	eceKey, err := hkdf.Key(sha256.New, ikm, auth, string(info), 32)
	if err != nil {
		t.Fatal(err)
	}
	cek, err := hkdf.Key(sha256.New, eceKey, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := hkdf.Key(sha256.New, eceKey, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := aes128GCM(cek)
	if err != nil {
		t.Fatal(err)
	}
	padded, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		t.Fatalf("AES-GCM Open: %v вЂ” С„РѕСЂРјР°С‚/РІС‹РІРѕРґ РєР»СЋС‡РµР№ РЅРµ СЃРѕРІРїР°РґР°РµС‚ СЃ RFC 8291", err)
	}
	if len(padded) == 0 || padded[len(padded)-1] != 2 {
		t.Fatalf("РЅРµС‚ РґРµР»РёРјРёС‚РµСЂР° 0x02 РІ РєРѕРЅС†Рµ: %v", padded)
	}
	plain := padded[:len(padded)-1]
	if string(plain) != string(payload) {
		t.Fatalf("СЂР°СЃС€РёС„СЂРѕРІР°РЅРЅС‹Р№ payload РЅРµ СЃРѕРІРїР°Р»:\n got %q\nwant %q", plain, payload)
	}
}

// TestVapidHeaderVerifiable — JWT из Authorization проверяется публичным
// ключом отправителя (то, что делает push-сервис), aud = origin endpoint'а.
func TestVapidHeaderVerifiable(t *testing.T) {
	s := newTestSender(t)
	const endpoint = "https://fcm.googleapis.com/fcm/send/token123"
	h, err := s.vapidHeader(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "vapid t=") || !strings.Contains(h, ", k=") {
		t.Fatalf("формат заголовка: %q", h)
	}
	parts := strings.Split(strings.TrimPrefix(h, "vapid t="), ", k=")
	if len(parts) != 2 {
		t.Fatalf("не удалось разобрать заголовок: %q", h)
	}
	segs := strings.Split(parts[0], ".")
	if len(segs) != 3 {
		t.Fatalf("JWT из %d сегментов, want 3", len(segs))
	}
	// Публичный ключ из k= должен совпадать с ключом отправителя.
	kRaw, err := b64.DecodeString(parts[1])
	if err != nil || string(kRaw) != string(s.pubRaw) {
		t.Fatal("k= не совпадает с публичным ключом отправителя")
	}
	// Подпись (ES256: r||s по 32 байта) проверяется этим ключом.
	sig, err := b64.DecodeString(segs[2])
	if err != nil || len(sig) != 64 {
		t.Fatalf("подпись %d байт, want 64", len(sig))
	}
	r := new(big.Int).SetBytes(sig[:32])
	ss := new(big.Int).SetBytes(sig[32:])
	digest := sha256.Sum256([]byte(segs[0] + "." + segs[1]))
	if !ecdsa.Verify(&s.signer.PublicKey, digest[:], r, ss) {
		t.Fatal("подпись VAPID JWT не прошла проверку публичным ключом")
	}
	// Claims: aud — origin endpoint'а.
	claimsJSON, err := b64.DecodeString(segs[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Aud != "https://fcm.googleapis.com" {
		t.Fatalf("aud = %q, want origin endpoint", claims.Aud)
	}
	if claims.Exp <= 0 || claims.Sub == "" {
		t.Fatalf("claims неполные: %+v", claims)
	}
}

// TestNewSenderKeyMismatch — пара ключей из разных генераций отклоняется:
// иначе рассылка молча уйдёт с публичным ключом, не соответствующим подписи.
func TestNewSenderKeyMismatch(t *testing.T) {
	pub1, _, _ := GenerateKeyPair()
	_, priv2, _ := GenerateKeyPair()
	if _, err := NewSender(pub1, priv2, ""); err == nil {
		t.Fatal("ожидалась ошибка несовпадения пары ключей")
	}
	if s, err := NewSender("", "", ""); err != nil || s != nil {
		t.Fatalf("пустые ключи: sender=%v err=%v, want nil,nil", s, err)
	}
}

// TestNotifyDeadEndpoint — 404/410 от провайдера возвращает endpoint на удаление.
func TestNotifyDeadEndpoint(t *testing.T) {
	s := newTestSender(t)
	uaPriv, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()
	dead := s.Notify(t.Context(), []Subscription{{
		Endpoint: srv.URL + "/push/abc",
		P256DH:   b64.EncodeToString(uaPriv.PublicKey().Bytes()),
		Auth:     b64.EncodeToString(auth),
	}}, Message{Title: "t"})
	if len(dead) != 1 {
		t.Fatalf("dead = %v, want один endpoint", dead)
	}
}
