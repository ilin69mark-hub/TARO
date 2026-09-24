// Package push — web-push инфра (см. U21, Could мес.4–6 задел).
// Подписки в push_subscriptions; публичный VAPID — GET /v1/push/public (ротация без ребилда).
// Отправка — stdlib (VAPID ES256 + RFC8188 aes128gcm), без внешних зависимостей.
// Использование cron/хуками — U25 (истечение), G1-аналог (вечерний пуш).
package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/hkdf"

	"taro/api/internal/apierr"
	"taro/api/internal/auth"
)

// Service — подписки и отправка.
type Service struct {
	pg   *pgxpool.Pool
	http *http.Client
}

// New возвращает сервис.
func New(pg *pgxpool.Pool) *Service {
	return &Service{pg: pg, http: &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			// Аудит B: dial-time блок private/loopback — закрывает rebinding-окно
			// между ревалидацией endpoint и фактическим соединением.
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil || len(addrs) == 0 {
					return nil, fmt.Errorf("dns fail")
				}
				for _, a := range addrs {
					if isBlockedIP(a.IP) {
						continue
					}
					d := &net.Dialer{Timeout: 5 * time.Second}
					return d.DialContext(ctx, network, net.JoinHostPort(a.IP.String(), port))
				}
				return nil, fmt.Errorf("blocked resolved ip")
			},
		},
	}}
}

// HandlePublicKey — GET /v1/push/public: VAPID-публичник (не секрет, см. U21).
func (s *Service) HandlePublicKey(w http.ResponseWriter, _ *http.Request) {
	key := os.Getenv("VAPID_PUBLIC_KEY")
	if key == "" {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Push не настроен")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"key": key})
}

type subRequest struct {
	Endpoint string `json:"endpoint"`
	P256DH   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

// validEndpoint — SSRF-allowlist (см. S02): только https, без userinfo,
// хост не private/loopback/link-local (проверяем и литерал, и DNS-ответ).
func validEndpoint(ctx context.Context, raw string) error {
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return fmt.Errorf("bad url")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("https only")
	}
	if u.User != nil {
		return fmt.Errorf("no userinfo")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("no host")
	}
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("blocked ip")
		}
		return nil
	}
	// DNS: все ответы должны быть публичными (защита от rebinding — на момент subscribe)
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addrs) == 0 {
		return fmt.Errorf("dns fail")
	}
	for _, a := range addrs {
		if isBlockedIP(a.IP) {
			return fmt.Errorf("blocked resolved ip")
		}
	}
	return nil
}

// isBlockedIP — private, loopback, link-local, multicast, unspecified.
func isBlockedIP(ip net.IP) bool {
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
}

// HandleSubscribe — POST /v1/push/subscribe: upsert по endpoint (SSRF-allowlist, см. S02).
func (s *Service) HandleSubscribe(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	var req subRequest
	if !apierr.Decode(w, r, &req) || req.Endpoint == "" || req.P256DH == "" || req.Auth == "" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нужны endpoint, p256dh, auth")
		return
	}
	if len(req.Endpoint) > 512 || len(req.P256DH) > 256 || len(req.Auth) > 128 ||
		strings.ContainsRune(req.Endpoint, 0) || strings.ContainsRune(req.P256DH, 0) || strings.ContainsRune(req.Auth, 0) {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Недопустимые данные подписки")
		return
	}
	if err := validEndpoint(r.Context(), req.Endpoint); err != nil {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Недопустимый endpoint")
		return
	}
	_, err := s.pg.Exec(r.Context(), `
		INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth) VALUES ($1,$2,$3,$4)
		ON CONFLICT (endpoint) DO UPDATE SET user_id=$1, p256dh=$3, auth=$4`,
		uid, req.Endpoint, req.P256DH, req.Auth)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось подписать")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// HandleUnsubscribe — DELETE /v1/push/unsubscribe {endpoint}: только своя.
func (s *Service) HandleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if !apierr.Decode(w, r, &req) || req.Endpoint == "" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нужен endpoint")
		return
	}
	_, _ = s.pg.Exec(r.Context(),
		`DELETE FROM push_subscriptions WHERE user_id=$1 AND endpoint=$2`, uid, req.Endpoint)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// Prefs — настройки пушей юзера (см. V24).
type Prefs struct {
	Hour  int  `json:"hour"`
	Quiet bool `json:"quiet"`
}

// HandleGetPrefs — GET /v1/push/prefs (дефолт 21:00, не quiet).
func (s *Service) HandleGetPrefs(w http.ResponseWriter, r *http.Request) {
	var p Prefs = Prefs{Hour: 21}
	_ = s.pg.QueryRow(r.Context(),
		`SELECT hour, quiet FROM push_preferences WHERE user_id=$1`,
		auth.UserID(r.Context())).Scan(&p.Hour, &p.Quiet)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(p)
}

// HandleSetPrefs — POST /v1/push/prefs {hour, quiet}.
func (s *Service) HandleSetPrefs(w http.ResponseWriter, r *http.Request) {
	var p Prefs
	if !apierr.Decode(w, r, &p) || p.Hour < 0 || p.Hour > 23 {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Час 0–23")
		return
	}
	_, err := s.pg.Exec(r.Context(), `
		INSERT INTO push_preferences (user_id, hour, quiet, updated_at) VALUES ($1,$2,$3,now())
		ON CONFLICT (user_id) DO UPDATE SET hour=$2, quiet=$3, updated_at=now()`,
		auth.UserID(r.Context()), p.Hour, p.Quiet)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось сохранить")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// eveningTemplates — 7 шаблонов вечернего пуша, ротация по дню недели без повтора (см. V23).
var eveningTemplates = []string{
	"Вечерняя карта дня уже ждет 🌙",
	"Минутка тишины: что скажут карты?",
	"Загляни в сегодняшний расклад ✨",
	"Карты соскучились. А ты?",
	"Вечерний ритуал: 2 минуты для себя 🌙",
	"Что подсвечивает сегодняшний день?",
	"Неделя идет — сверься с картами ✨",
}

// eveningFor возвращает шаблон по дню недели МСК.
func eveningFor(now time.Time) string {
	msk, _ := time.LoadLocation("Europe/Moscow")
	if msk == nil {
		msk = time.FixedZone("MSK", 3*3600)
	}
	wd := int(now.In(msk).Weekday()) // 0=вс
	return eveningTemplates[wd%len(eveningTemplates)]
}

// eveningTargets — подписчики с hour=текущий час МСК и не quiet (см. V24).
func (s *Service) eveningTargets(ctx context.Context) ([]string, error) {
	msk, _ := time.LoadLocation("Europe/Moscow")
	if msk == nil {
		msk = time.FixedZone("MSK", 3*3600)
	}
	hour := time.Now().In(msk).Hour()
	rows, err := s.pg.Query(ctx, `
		SELECT DISTINCT ps.user_id FROM push_subscriptions ps
		LEFT JOIN push_preferences pp ON pp.user_id=ps.user_id
		WHERE COALESCE(pp.quiet,false)=false AND COALESCE(pp.hour,21)=$1`, hour)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		_ = rows.Scan(&u)
		out = append(out, u)
	}
	return out, rows.Err()
}

// HandleEvening — POST /v1/admin/push-evening: вечерняя рассылка по hour/quite (см. V23/V24).
func (s *Service) HandleEvening(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	users, err := s.eveningTargets(ctx)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось выбрать")
		return
	}
	sent := 0
	for _, u := range users {
		n, _ := s.SendToUser(ctx, u, "Онлайн Таро", eveningFor(time.Now()))
		sent += n
		_, _ = s.pg.Exec(ctx,
			`INSERT INTO push_logs (user_id, kind, status) VALUES ($1,'evening','sent')`, u)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "users": len(users), "sent": sent})
}

// HandleStreakRisk — POST /v1/admin/push-streak-risk: стрик≥2 с активностью только вчера (см. V25).
func (s *Service) HandleStreakRisk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.pg.Query(ctx, `
		SELECT user_id FROM (
		  SELECT user_id, MAX((created_at AT TIME ZONE 'Europe/Moscow')::date) AS last_day,
		         COUNT(DISTINCT (created_at AT TIME ZONE 'Europe/Moscow')::date) AS days
		    FROM (SELECT user_id, created_at FROM readings WHERE status='done'
		          UNION ALL SELECT user_id, created_at FROM diary_entries) t
		   GROUP BY user_id
		) s WHERE last_day = (now() AT TIME ZONE 'Europe/Moscow')::date - 1 AND days >= 1`)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось выбрать")
		return
	}
	defer rows.Close()
	sent, users := 0, 0
	for rows.Next() {
		var u string
		_ = rows.Scan(&u)
		users++
		n, _ := s.SendToUser(ctx, u, "Стрик в опасности 🔥", "Загляни сегодня — не прерывай серию!")
		sent += n
		_, _ = s.pg.Exec(ctx,
			`INSERT INTO push_logs (user_id, kind, status) VALUES ($1,'streak_risk','sent')`, u)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "users": users, "sent": sent})
}

// HandlePushStats — GET /v1/admin/push-stats: отправки за 7д по видам (см. V26, без трекинг-пикселя).
func (s *Service) HandlePushStats(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pg.Query(r.Context(), `
		SELECT kind, COUNT(*), MAX(created_at) FROM push_logs
		 WHERE created_at > now() - interval '7 days' GROUP BY kind ORDER BY 2 DESC`)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось")
		return
	}
	defer rows.Close()
	type stat struct {
		Kind  string `json:"kind"`
		Count int    `json:"count"`
		Last  string `json:"last"`
	}
	out := []stat{}
	for rows.Next() {
		var st stat
		var ts time.Time
		_ = rows.Scan(&st.Kind, &st.Count, &ts)
		st.Last = ts.Format(time.RFC3339)
		out = append(out, st)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// RemindExpiring шлет пуш юзерам с подпиской, истекающей в ближайшие 72ч (см. U25).
// Возвращает (напомнили, всего истекающих). Вызывать кроном 1 раз/день (см. T30 cron).
func (s *Service) RemindExpiring(ctx context.Context) (int, int, error) {
	rows, err := s.pg.Query(ctx, `
		SELECT DISTINCT user_id FROM subscriptions
		 WHERE status='active' AND valid_until > now() AND valid_until < now() + interval '72 hours'`)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	var users []string
	for rows.Next() {
		var u string
		_ = rows.Scan(&u)
		users = append(users, u)
	}
	sent := 0
	for _, u := range users {
		n, _ := s.SendToUser(ctx, u, "Безлимит скоро закончится", "Продли в 1 тап — карты уже ждут вечером 🌙")
		sent += n
	}
	return sent, len(users), nil
}

// HandleRemindExpiring — POST /v1/admin/remind-expiring (только :8081, см. U25).
func (s *Service) HandleRemindExpiring(w http.ResponseWriter, r *http.Request) {
	sent, total, err := s.RemindExpiring(r.Context())
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось разослать")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"reminded": sent, "expiring": total})
}

// Subscription — одна push-подписка.
type Subscription struct {
	Endpoint string
	P256DH   string
	Auth     string
}

// userSubs загружает подписки юзера.
func (s *Service) userSubs(ctx context.Context, userID string) ([]Subscription, error) {
	rows, err := s.pg.Query(ctx,
		`SELECT endpoint, p256dh, auth FROM push_subscriptions WHERE user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Subscription
	for rows.Next() {
		var sub Subscription
		if err := rows.Scan(&sub.Endpoint, &sub.P256DH, &sub.Auth); err != nil {
			continue
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// SendToUser шлет title/body всем подпискам юзера; мертвые endpoint (404/410) чистит.
func (s *Service) SendToUser(ctx context.Context, userID, title, body string) (sent int, err error) {
	subs, err := s.userSubs(ctx, userID)
	if err != nil {
		return 0, err
	}
	payload, _ := json.Marshal(map[string]string{"title": title, "body": body})
	for _, sub := range subs {
		st, serr := s.send(ctx, sub, payload)
		if serr != nil {
			continue
		}
		if st == http.StatusNotFound || st == http.StatusGone {
			_, _ = s.pg.Exec(ctx, `DELETE FROM push_subscriptions WHERE endpoint=$1`, sub.Endpoint)
			continue
		}
		if st >= 200 && st < 300 {
			sent++
		}
	}
	return sent, nil
}

// vapidKey читает приватник.
func vapidKey() (*ecdsa.PrivateKey, error) {
	raw := os.Getenv("VAPID_PRIVATE_KEY")
	if raw == "" {
		return nil, fmt.Errorf("no vapid key")
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(b) != 32 {
		return nil, fmt.Errorf("bad vapid key")
	}
	priv := new(ecdsa.PrivateKey)
	priv.PublicKey.Curve = elliptic.P256()
	priv.D = new(big.Int).SetBytes(b)
	priv.PublicKey.X, priv.PublicKey.Y = priv.PublicKey.Curve.ScalarBaseMult(b)
	return priv, nil
}

// vapidJWT строит Authorization: vapid t=...,k=...
func vapidJWT(priv *ecdsa.PrivateKey, aud string) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{
		"aud": aud, "exp": time.Now().Add(12 * time.Hour).Unix(), "sub": "mailto:admin@taro.local",
	})
	payload := base64.RawURLEncoding.EncodeToString(claims)
	hash := sha256.Sum256([]byte(header + "." + payload))
	r, sSig, err := ecdsa.Sign(rand.Reader, priv, hash[:])
	if err != nil {
		return "", err
	}
	rb := r.Bytes()
	sb := sSig.Bytes()
	sig := make([]byte, 64)
	copy(sig[32-len(rb):32], rb)
	copy(sig[64-len(sb):], sb)
	return header + "." + payload + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// encryptPayload — RFC8188 aes128gcm: ECDH ephemeral×client, HKDF, AES-GCM, pad 0x02.
func encryptPayload(clientP256dh, authSecret, plaintext []byte) (pub, salt, body []byte, err error) {
	eph, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	raw := eph.PublicKey().Bytes() // 65 байт uncompressed — валидная точка
	clientKey, err := ecdh.P256().NewPublicKey(clientP256dh)
	shared, err := eph.ECDH(clientKey)
	if err != nil {
		return nil, nil, nil, err
	}
	authB := authSecret
	info := append(append([]byte("WebPush: info\x00"), clientP256dh...), raw...)
	prk := hkdfExtract(authB, shared)
	ikm, _ := hkdfExpand(prk, info, 32)
	salt = make([]byte, 16)
	if _, err = rand.Read(salt); err != nil {
		return nil, nil, nil, err
	}
	prk2 := hkdfExtract(salt, ikm)
	cek, _ := hkdfExpand(prk2, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce, _ := hkdfExpand(prk2, []byte("Content-Encoding: nonce\x00"), 12)
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, nil, err
	}
	padded := append([]byte{0x02}, plaintext...)
	// паддинг до блока кратно? aesgcm без паддинга ок, но добавим выравнивание 16
	if r := len(padded) % 16; r != 0 {
		padded = append(padded, make([]byte, 16-r)...)
	}
	ct := gcm.Seal(nil, nonce, padded, nil)
	return raw, salt, ct, nil
}

func hkdfExtract(salt, ikm []byte) []byte {
	mac := hkdf.Extract(sha256.New, ikm, salt)
	return mac
}

func hkdfExpand(prk, info []byte, n int) ([]byte, error) {
	out := make([]byte, n)
	r := hkdf.Expand(sha256.New, prk, info)
	_, err := r.Read(out)
	return out, err
}

// send собирает и шлет один пуш. Возвращает HTTP-статус.
func (s *Service) send(ctx context.Context, sub Subscription, payload []byte) (int, error) {
	// Аудит B: ревалидация перед каждой отправкой (подписка могла перевесить DNS — rebinding).
	if err := validEndpoint(ctx, sub.Endpoint); err != nil {
		return 0, err
	}
	priv, err := vapidKey()
	if err != nil {
		return 0, err
	}
	u, err := url.Parse(sub.Endpoint)
	if err != nil {
		return 0, err
	}
	aud := u.Scheme + "://" + u.Host
	token, err := vapidJWT(priv, aud)
	if err != nil {
		return 0, err
	}
	pubBytes, err := base64.RawURLEncoding.DecodeString(sub.P256DH)
	if err != nil {
		return 0, err
	}
	authBytes, err := base64.RawURLEncoding.DecodeString(sub.Auth)
	if err != nil {
		return 0, err
	}
	pub, salt, body, err := encryptPayload(pubBytes, authBytes, payload)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Encryption", "salt="+base64.RawURLEncoding.EncodeToString(salt))
	dh := base64.RawURLEncoding.EncodeToString(pub)
	req.Header.Set("Crypto-Key", "dh="+dh+";p256ecdsa="+base64.RawURLEncoding.EncodeToString(uncompressedPub(priv)))
	req.Header.Set("Authorization", "vapid t="+token+", k="+base64.RawURLEncoding.EncodeToString(uncompressedPub(priv)))
	resp, err := s.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// uncompressedPub — 65-байтный паблик VAPID.
func uncompressedPub(priv *ecdsa.PrivateKey) []byte {
	xb := priv.PublicKey.X.Bytes()
	yb := priv.PublicKey.Y.Bytes()
	out := make([]byte, 65)
	out[0] = 0x04
	copy(out[1+32-len(xb):33], xb)
	copy(out[33+32-len(yb):], yb)
	return out
}
