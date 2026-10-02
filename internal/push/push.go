// Package push реализует Web Push (RFC 8030 — доставка, RFC 8291 — шифрование
// полезной нагрузки aes128gcm, RFC 8292 — VAPID-авторизация) без внешних
// зависимостей: только стандартная библиотека. Пакет отвечает за протокол;
// подписки хранятся в store, HTTP-обвязка — в httpapi.
//
// Криптография намеренно на crypto/ecdh + crypto/hkdf (stdlib, Go 1.24):
// серверная пара ключей одна и та же для ECDH и подписи VAPID-JWT — так делают
// и основные web-push-библиотеки.
package push

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var b64 = base64.RawURLEncoding

// Subscription — данные подписки браузера (PushSubscription.toJSON()).
type Subscription struct {
	Endpoint string
	P256DH   string // base64url, uncompressed P-256, 65 байт
	Auth     string // base64url, 16 байт
}

// Message — полезная нагрузка уведомления; сериализуется в JSON и шифруется.
type Message struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
}

// Sender рассылает зашифрованные пуши на endpoint'ы подписок.
// Ниль-указатель = Web Push выключен (ключи VAPID не заданы).
type Sender struct {
	client  *http.Client
	dh      *ecdh.PrivateKey  // для ECDH (RFC 8291)
	signer  *ecdsa.PrivateKey // для VAPID JWT (ES256) — тот же ключ
	pubRaw  []byte            // uncompressed public key, 65 байт
	subject string            // mailto: для VAPID
}

// GenerateKeyPair создаёт новую пару VAPID-ключей (используется cmd/vapidkeygen).
func GenerateKeyPair() (publicB64, privateB64 string, err error) {
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return b64.EncodeToString(priv.PublicKey().Bytes()), b64.EncodeToString(priv.Bytes()), nil
}

// NewSender собирает отправитель из конфигурации. Оба ключа пустые —
// возвращается (nil, nil): push выключен. Один задан, другой нет — ошибка:
// рассинхрон ключей сломает все подписки.
func NewSender(publicKeyB64, privateKeyB64, subject string) (*Sender, error) {
	if publicKeyB64 == "" && privateKeyB64 == "" {
		return nil, nil
	}
	d, err := b64.DecodeString(strings.TrimSpace(privateKeyB64))
	if err != nil || len(d) != 32 {
		return nil, errors.New("push: VAPID_PRIVATE_KEY должен быть base64url 32-байтового скаляра P-256 (см. go run ./cmd/vapidkeygen)")
	}
	priv, err := ecdh.P256().NewPrivateKey(d)
	if err != nil {
		return nil, fmt.Errorf("push: приватный ключ VAPID некорректен: %w", err)
	}
	pubRaw := priv.PublicKey().Bytes()
	if want, err := b64.DecodeString(strings.TrimSpace(publicKeyB64)); err != nil || string(want) != string(pubRaw) {
		return nil, errors.New("push: VAPID_PUBLIC_KEY не соответствует VAPID_PRIVATE_KEY (пара должна быть из одной генерации)")
	}
	if subject == "" {
		subject = "mailto:admin@otklik.local"
	}
	// Подпись VAPID JWT — ES256 тем же ключом: собираем ecdsa.PrivateKey из
	// скаляра и uncompressed-точки (конвертера crypto/ecdh → crypto/ecdsa нет).
	x, y := elliptic.Unmarshal(elliptic.P256(), pubRaw)
	if x == nil {
		return nil, errors.New("push: не удалось разобрать публичный ключ VAPID")
	}
	signer := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y},
		D:         new(big.Int).SetBytes(d),
	}
	return &Sender{
		client:  &http.Client{Timeout: 15 * time.Second},
		dh:      priv,
		signer:  signer,
		pubRaw:  pubRaw,
		subject: subject,
	}, nil
}

// aes128GCM — обёртка над AES-128-GCM (key из HKDF, 16 байт).
func aes128GCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// encryptFor шифрует payload по RFC 8291 (aes128gcm) для конкретной подписки:
//
//	ikm     = ECDH(server_priv, ua_pub)
//	ece_key = HKDF(ikm, salt=auth,  info="WebPush: info\0" || ua_pub || server_pub, 32)
//	cek     = HKDF(ece_key, salt, info="Content-Encoding: aes128gcm\0", 16)
//	nonce   = HKDF(ece_key, salt, info="Content-Encoding: nonce\0", 12)
//	body    = salt(16) || rs(4) || keylen(1)=65 || server_pub(65) || AES-GCM(padded)
func (s *Sender) encryptFor(payload []byte, sub Subscription) ([]byte, error) {
	uaRaw, err := b64.DecodeString(strings.TrimSpace(sub.P256DH))
	if err != nil || len(uaRaw) != 65 || uaRaw[0] != 4 {
		return nil, errors.New("push: некорректный ключ подписки p256dh")
	}
	auth, err := b64.DecodeString(strings.TrimSpace(sub.Auth))
	if err != nil || len(auth) < 16 {
		return nil, errors.New("push: некорректный auth-секрет подписки")
	}
	uaPub, err := ecdh.P256().NewPublicKey(uaRaw)
	if err != nil {
		return nil, fmt.Errorf("push: p256dh: %w", err)
	}
	ikm, err := s.dh.ECDH(uaPub)
	if err != nil {
		return nil, fmt.Errorf("push: ecdh: %w", err)
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	info := make([]byte, 0, len("WebPush: info\x00")+len(uaRaw)+len(s.pubRaw))
	info = append(info, "WebPush: info\x00"...)
	info = append(info, uaRaw...)
	info = append(info, s.pubRaw...)
	eceKey, err := hkdf.Key(sha256.New, ikm, auth, string(info), 32)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Key(sha256.New, eceKey, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Key(sha256.New, eceKey, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}

	gcm, err := aes128GCM(cek)
	if err != nil {
		return nil, err
	}
	padded := append(append([]byte{}, payload...), 2) // делимитер 0x02, без паддинга
	ct := gcm.Seal(nil, nonce, padded, nil)

	const rs = 4096 // размер записи: обязателен в заголовке aes128gcm
	body := make([]byte, 0, 21+len(s.pubRaw)+len(ct))
	body = append(body, salt...)
	var rsBuf [4]byte
	binary.BigEndian.PutUint32(rsBuf[:], rs)
	body = append(body, rsBuf[:]...)
	body = append(body, byte(len(s.pubRaw)))
	body = append(body, s.pubRaw...)
	body = append(body, ct...)
	return body, nil
}

// vapidHeader строит Authorization: vapid t=<JWT>, k=<pub> (RFC 8292).
// JWT подписан ES256 тем же ключом; aud — origin endpoint'а.
func (s *Sender) vapidHeader(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("push: некорректный endpoint")
	}
	header := `{"typ":"JWT","alg":"ES256"}`
	claims, err := json.Marshal(struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}{u.Scheme + "://" + u.Host, time.Now().Add(12 * time.Hour).Unix(), s.subject})
	if err != nil {
		return "", err
	}
	signing := b64.EncodeToString([]byte(header)) + "." + b64.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))
	r, sigS, err := ecdsa.Sign(rand.Reader, s.signer, digest[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	sigS.FillBytes(sig[32:])
	return "vapid t=" + signing + "." + b64.EncodeToString(sig) +
		", k=" + b64.EncodeToString(s.pubRaw), nil
}

// errGone — провайдер ответил 404/410: подписка отозвана, её нужно удалить.
var errGone = errors.New("push: subscription expired (404/410)")

// Notify доставляет сообщение каждой подписке и возвращает endpoint'ы,
// на которые сервис-провайдер ответил 404/410. Остальные ошибки логируются,
// но не роняют рассылку: один мёртвый endpoint не блокирует остальные.
func (s *Sender) Notify(ctx context.Context, subs []Subscription, m Message) (dead []string) {
	payload, err := json.Marshal(m)
	if err != nil {
		slog.Error("push: marshal payload", "error", err)
		return nil
	}
	for _, sub := range subs {
		if err := s.send(ctx, payload, sub); err != nil {
			if errors.Is(err, errGone) {
				dead = append(dead, sub.Endpoint)
				continue
			}
			slog.Warn("push: delivery failed", "error", err.Error())
		}
	}
	return dead
}

func (s *Sender) send(ctx context.Context, payload []byte, sub Subscription) error {
	body, err := s.encryptFor(payload, sub)
	if err != nil {
		return err
	}
	auth, err := s.vapidHeader(sub.Endpoint)
	if err != nil {
		return err
	}
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, sub.Endpoint, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("TTL", "43200")      // 12 часов: сообщение доехав до телефона
	req.Header.Set("Urgency", "normal") // бережёт батарею
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Authorization", auth)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	switch {
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated:
		return nil
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return errGone
	default:
		return fmt.Errorf("push service answered %s", resp.Status)
	}
}
