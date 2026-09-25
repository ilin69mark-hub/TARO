// Package auth — Telegram ID + UUID anon (см. docs/project-book/02-functional/02).
// JWT cookie taro_jwt: HttpOnly; Secure; SameSite=None + CSRF-header; Path=/; 30д.
// Trial 3д выдается 1 раз на связку tg_id+fingerprint (см. T09, 02-functional/05).
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"taro/api/internal/apierr"
)

const (
	// CookieName — имя user-JWT (см. 04-api-spec.md).
	CookieName = "taro_jwt"
	// UserTTL — 30 дней (см. 02-auth.md).
	UserTTL = 30 * 24 * time.Hour
	// AnonRegPerIPPerHour — антиферма: 20 reg/час/IP + капча с 21-й (см. 02-auth.md).
	AnonRegPerIPPerHour = 20
	TelegramAuthMaxAge  = 10 * time.Minute
	maxFingerprintLen   = 64
)

// Service — auth-операции.
type Service struct {
	pg *pgxpool.Pool
	rd *redis.Client
}

// New возвращает auth-сервис.
func New(pg *pgxpool.Pool, rd *redis.Client) *Service {
	return &Service{pg: pg, rd: rd}
}

// VerifyInitData проверяет подпись Telegram WebApp initData (HMAC-SHA256, ключ WebAppData).
// Возвращает tg_id. auth_date проверяется на будущее и короткое окно свежести.
// S01 fail-closed: пустой botToken отвергается, кроме явного TG_ALLOW_EMPTY=1 (dev/тесты).
func VerifyInitData(initData, botToken string) (int64, error) {
	if botToken == "" && os.Getenv("TG_ALLOW_EMPTY") != "1" {
		return 0, fmt.Errorf("no bot token")
	}
	if botToken == "dev-only-bot" && os.Getenv("TG_ALLOW_DEV_BOT") != "1" {
		return 0, fmt.Errorf("dev bot token запрещён в проде")
	}
	q, err := url.ParseQuery(initData)
	if err != nil {
		return 0, fmt.Errorf("bad initData: %w", err)
	}
	gotHash := q.Get("hash")
	if gotHash == "" {
		return 0, fmt.Errorf("missing hash")
	}
	// auth_date обязателен и свеж (S01: без него initData вечный)
	ts := q.Get("auth_date")
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || ts == "" {
		return 0, fmt.Errorf("missing auth_date")
	}
	authAt := time.Unix(unix, 0)
	now := time.Now()
	if authAt.After(now) {
		return 0, fmt.Errorf("future auth_date")
	}
	if now.Sub(authAt) > TelegramAuthMaxAge {
		return 0, fmt.Errorf("stale auth_date")
	}
	pairs := []string{}
	for k, vv := range q {
		if k == "hash" {
			continue
		}
		pairs = append(pairs, k+"="+strings.Join(vv, ","))
	}
	sort.Strings(pairs)
	check := strings.Join(pairs, "\n")
	macKey := hmac.New(sha256.New, []byte("WebAppData"))
	macKey.Write([]byte(botToken))
	mac := hmac.New(sha256.New, macKey.Sum(nil))
	mac.Write([]byte(check))
	wantHash, err := hex.DecodeString(gotHash)
	if err != nil || !hmac.Equal(wantHash, mac.Sum(nil)) {
		return 0, fmt.Errorf("bad hash")
	}
	var user struct {
		ID int64 `json:"id"`
	}
	raw := q.Get("user")
	if raw == "" {
		return 0, fmt.Errorf("missing user")
	}
	if err := json.Unmarshal([]byte(raw), &user); err != nil {
		return 0, fmt.Errorf("bad user: %w", err)
	}
	if user.ID == 0 {
		return 0, fmt.Errorf("missing user id")
	}
	return user.ID, nil
}

type tokenClaims struct {
	Subject string
	SID     string
	JTI     string
	Legacy  bool
}

func newTokenID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func jwtSecret() ([]byte, error) {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" || secret == "dev-only-secret" || len(secret) < 32 {
		return nil, fmt.Errorf("JWT_SECRET обязателен (длина >=32, без dev-значений)")
	}
	return []byte(secret), nil
}

func IssueJWT(userID string, ttl time.Duration, sessionID ...string) (string, error) {
	secret, err := jwtSecret()
	if err != nil {
		return "", err
	}
	if userID == "" || ttl <= 0 {
		return "", fmt.Errorf("invalid token parameters")
	}
	jti, err := newTokenID()
	if err != nil {
		return "", err
	}
	sid := ""
	legacy := len(sessionID) == 0
	if legacy {
		sid, err = newTokenID()
		if err != nil {
			return "", err
		}
	} else {
		sid = sessionID[0]
	}
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":    userID,
		"exp":    now.Add(ttl).Unix(),
		"iat":    now.Unix(),
		"jti":    jti,
		"sid":    sid,
		"legacy": legacy,
	})
	return tok.SignedString(secret)
}

func parseJWT(token string) (tokenClaims, error) {
	secret, err := jwtSecret()
	if err != nil {
		return tokenClaims{}, err
	}
	tok, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("bad alg")
		}
		return secret, nil
	})
	if err != nil || !tok.Valid {
		return tokenClaims{}, fmt.Errorf("bad token")
	}
	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return tokenClaims{}, fmt.Errorf("bad claims")
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return tokenClaims{}, fmt.Errorf("no sub")
	}
	sid, _ := claims["sid"].(string)
	jti, _ := claims["jti"].(string)
	legacy, _ := claims["legacy"].(bool)
	return tokenClaims{Subject: sub, SID: sid, JTI: jti, Legacy: legacy}, nil
}

type SessionClaims struct {
	Subject string
	SID     string
	JTI     string
}

func ParseJWTClaims(token string) (SessionClaims, error) {
	claims, err := parseJWT(token)
	if err != nil {
		return SessionClaims{}, err
	}
	return SessionClaims{Subject: claims.Subject, SID: claims.SID, JTI: claims.JTI}, nil
}

func ParseJWT(token string) (string, error) {
	claims, err := parseJWT(token)
	if err != nil {
		return "", err
	}
	return claims.Subject, nil
}

// writeCookie кладет JWT в cookie: HttpOnly; Secure; SameSite=None; Path=/.
// SameSite=None обязателен — иначе TG WebApp iframe режет cookie (см. 03-nonfunctional/02).
// CSRF: все POST требуют заголовок X-CSRF (проверка — T10 middleware; TODO).
func writeCookie(w http.ResponseWriter, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
	})
}

// FpCookie — httpOnly-флаг fingerprint (аудит B: fp жил в localStorage рядом с uuid,
// один XSS забирал пару и входил как жертва). Сервер ставит после успешного входа;
// клиент шлёт из памяти, localStorage — только legacy-источник до первого успеха.
const FpCookie = "taro_fp"

// writeFpCookie кладет fingerprint в httpOnly cookie (флаги как у сессии — TG iframe).
func writeFpCookie(w http.ResponseWriter, fp string) {
	if fp == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     FpCookie,
		Value:    fp,
		Path:     "/",
		MaxAge:   int(UserTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
	})
}

func fpOf(r *http.Request, bodyFp string) string {
	if c, err := r.Cookie(FpCookie); err == nil && c.Value != "" {
		return c.Value
	}
	return bodyFp
}

func validFingerprint(fp string, required bool) bool {
	if fp == "" {
		return !required
	}
	if len(fp) > maxFingerprintLen || strings.TrimSpace(fp) == "" {
		return false
	}
	for i := 0; i < len(fp); i++ {
		if fp[i] < 0x21 || fp[i] > 0x7e {
			return false
		}
	}
	return true
}

func fingerprintsEqual(a, b string) bool {
	ah := sha256.Sum256([]byte(a))
	bh := sha256.Sum256([]byte(b))
	return hmac.Equal(ah[:], bh[:])
}

// telegramRequest — POST /v1/auth/telegram {initData, fingerprint?}.
func (s *Service) HandleTelegram(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InitData    string `json:"initData"`
		Fingerprint string `json:"fingerprint"`
	}
	if !apierr.Decode(w, r, &req) || req.InitData == "" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректный initData")
		return
	}
	fp := fpOf(r, req.Fingerprint)
	if !validFingerprint(fp, false) {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректный fingerprint")
		return
	}
	id, isNew, trialDays, err := s.TelegramLogin(r.Context(), req.InitData, fp)
	if err != nil {
		apierr.Write(w, http.StatusUnauthorized, apierr.CodeInvalidTg, "Не удалось подтвердить Telegram")
		return
	}
	tok, err := s.issueSession(r.Context(), id, UserTTL)
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
		return
	}
	csrf, err := s.issueCSRF(w, r.Context(), id)
	if err != nil {
		if s.rd != nil {
			_ = s.rd.Del(r.Context(), sessKey(id), "csrf:"+id).Err()
		}
		ExpireAuthCookies(w)
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
		return
	}
	writeCookie(w, tok, UserTTL)
	writeFpCookie(w, fpOf(r, req.Fingerprint))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"user_id": id, "is_new": isNew, "trial_days": trialDays, "csrf_token": csrf})
}

// anonRequest — POST /v1/auth/anon {uuid, fingerprint?}. IP — из X-Real-IP (ставит nginx).
func (s *Service) HandleAnon(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UUID        string `json:"uuid"`
		Fingerprint string `json:"fingerprint"`
	}
	if !apierr.Decode(w, r, &req) || req.UUID == "" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректный uuid")
		return
	}
	// S08: мусор вместо UUID (ферма на случайных строках) → 422 до БД
	if !isUUID(req.UUID) {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректный uuid")
		return
	}
	fp := fpOf(r, req.Fingerprint)
	if !validFingerprint(fp, false) {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректный fingerprint")
		return
	}
	ip := r.Header.Get("X-Real-IP")
	if ip == "" {
		ip = "unknown"
	}
	id, err := s.AnonLogin(r.Context(), req.UUID, fp, ip)
	if err != nil {
		if err.Error() == "rate_limited" {
			apierr.Write(w, http.StatusTooManyRequests, apierr.CodeRateLimited, "Слишком много регистраций, попробуй позже")
			return
		}
		if err.Error() == "fp_mismatch" {
			apierr.Write(w, http.StatusForbidden, "FP_MISMATCH", "Устройство не узнано, войди через Telegram")
			return
		}
		if err.Error() == "fingerprint_required" {
			apierr.Write(w, http.StatusForbidden, "FP_REQUIRED", "Нужен fingerprint устройства")
			return
		}
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать сессию")
		return
	}
	tok, err := s.issueSession(r.Context(), id, UserTTL)
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
		return
	}
	csrf, err := s.issueCSRF(w, r.Context(), id)
	if err != nil {
		if s.rd != nil {
			_ = s.rd.Del(r.Context(), sessKey(id), "csrf:"+id).Err()
		}
		ExpireAuthCookies(w)
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
		return
	}
	writeCookie(w, tok, UserTTL)
	writeFpCookie(w, fpOf(r, req.Fingerprint))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"user_id": id, "csrf_token": csrf})
}

func (s *Service) TelegramLogin(ctx context.Context, initData, fingerprint string) (string, bool, int, error) {
	if !validFingerprint(fingerprint, false) {
		return "", false, 0, fmt.Errorf("invalid fingerprint")
	}
	tgID, err := VerifyInitData(initData, os.Getenv("TG_BOT_TOKEN"))
	if err != nil {
		return "", false, 0, err
	}
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return "", false, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, tgID); err != nil {
		return "", false, 0, err
	}
	var id, storedFP, status string
	created := false
	err = tx.QueryRow(ctx, `SELECT id, COALESCE(fingerprint,''), status FROM users WHERE tg_id=$1 FOR UPDATE`, tgID).Scan(&id, &storedFP, &status)
	if err == pgx.ErrNoRows {
		err = tx.QueryRow(ctx,
			`INSERT INTO users (tg_id, fingerprint) VALUES ($1,$2) ON CONFLICT (tg_id) WHERE tg_id IS NOT NULL DO NOTHING RETURNING id`, tgID, fingerprint).Scan(&id)
		if err == nil {
			created = true
			storedFP = fingerprint
			status = "active"
		} else if err == pgx.ErrNoRows {
			err = tx.QueryRow(ctx, `SELECT id, COALESCE(fingerprint,''), status FROM users WHERE tg_id=$1 FOR UPDATE`, tgID).Scan(&id, &storedFP, &status)
		}
	}
	if err != nil {
		return "", false, 0, err
	}
	if status != "active" {
		return "", false, 0, fmt.Errorf("user inactive")
	}
	if !created {
		if storedFP == "" && fingerprint != "" {
			if _, err := tx.Exec(ctx, `UPDATE users SET fingerprint=$1 WHERE id=$2`, fingerprint, id); err != nil {
				return "", false, 0, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return "", false, 0, err
		}
		return id, false, 0, nil
	}
	granted, days, err := grantTrialTx(ctx, tx, id, tgID, fingerprint)
	if err != nil {
		return "", false, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", false, 0, err
	}
	if !granted {
		return id, true, 0, nil
	}
	return id, true, days, nil
}

// isUUID проверяет формат 8-4-4-4-12 hex без внешних зависимостей (см. S08).
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}

func (s *Service) AnonLogin(ctx context.Context, uuid, fingerprint, ip string) (string, error) {
	if fingerprint == "" {
		return "", fmt.Errorf("fingerprint_required")
	}
	if !validFingerprint(fingerprint, true) {
		return "", fmt.Errorf("invalid fingerprint")
	}
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id, storedFP, status string
	err = tx.QueryRow(ctx, `SELECT id, COALESCE(fingerprint,''), status FROM users WHERE anon_uuid=$1 FOR UPDATE`, uuid).Scan(&id, &storedFP, &status)
	if err == nil {
		if status != "active" {
			return "", fmt.Errorf("user inactive")
		}
		if fingerprint == "" {
			return "", fmt.Errorf("fingerprint_required")
		}
		if storedFP != "" && !fingerprintsEqual(storedFP, fingerprint) {
			return "", fmt.Errorf("fp_mismatch")
		}
		if storedFP == "" {
			if _, err := tx.Exec(ctx, `UPDATE users SET fingerprint=$1 WHERE id=$2`, fingerprint, id); err != nil {
				return "", err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		return id, nil
	}
	if err != pgx.ErrNoRows {
		return "", err
	}
	key := "rl:reg:" + ip
	n, err := s.rd.Eval(ctx,
		`local n = redis.call('INCR', KEYS[1]); if n == 1 then redis.call('EXPIRE', KEYS[1], ARGV[1]) end; return n`,
		[]string{key}, 3600).Int()
	if err != nil {
		return "", err
	}
	if n > AnonRegPerIPPerHour {
		return "", fmt.Errorf("rate_limited")
	}
	err = tx.QueryRow(ctx,
		`INSERT INTO users (anon_uuid, fingerprint) VALUES ($1,$2) ON CONFLICT (anon_uuid) WHERE anon_uuid IS NOT NULL DO NOTHING RETURNING id`, uuid, fingerprint).Scan(&id)
	if err == pgx.ErrNoRows {
		err = tx.QueryRow(ctx, `SELECT id, COALESCE(fingerprint,''), status FROM users WHERE anon_uuid=$1 FOR UPDATE`, uuid).Scan(&id, &storedFP, &status)
		if err != nil {
			return "", err
		}
		if status != "active" {
			return "", fmt.Errorf("user inactive")
		}
		if fingerprint == "" {
			return "", fmt.Errorf("fingerprint_required")
		}
		if storedFP != "" && !fingerprintsEqual(storedFP, fingerprint) {
			return "", fmt.Errorf("fp_mismatch")
		}
		if storedFP == "" {
			if _, err := tx.Exec(ctx, `UPDATE users SET fingerprint=$1 WHERE id=$2`, fingerprint, id); err != nil {
				return "", err
			}
		}
	}
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func parseTrialConfig(raw json.RawMessage) (bool, int) {
	var cfg struct {
		Enabled *bool `json:"enabled"`
		Days    *int  `json:"days"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return true, 3
	}
	enabled := true
	if cfg.Enabled != nil {
		enabled = *cfg.Enabled
	}
	days := 3
	if cfg.Days != nil {
		days = *cfg.Days
	}
	if days < 1 || days > 365 {
		days = 3
	}
	return enabled, days
}

func trialSettings(ctx context.Context, q rowQuerier) (bool, int, error) {
	var raw json.RawMessage
	err := q.QueryRow(ctx, `SELECT value FROM app_config WHERE key='trial'`).Scan(&raw)
	if err == pgx.ErrNoRows {
		return true, 3, nil
	}
	if err != nil {
		return false, 0, err
	}
	enabled, days := parseTrialConfig(raw)
	return enabled, days, nil
}

func grantTrialTx(ctx context.Context, tx pgx.Tx, userID string, tgID int64, fingerprint string) (bool, int, error) {
	enabled, days, err := trialSettings(ctx, tx)
	if err != nil || !enabled {
		return false, 0, err
	}
	tag, err := tx.Exec(ctx,
		`INSERT INTO trial_grants (tg_id, fingerprint, user_id) VALUES ($1,$2,$3)
		 ON CONFLICT (tg_id) DO NOTHING`, tgID, fingerprint, userID)
	if err != nil {
		return false, 0, err
	}
	if tag.RowsAffected() == 0 {
		return false, 0, nil
	}
	var planID string
	if err := tx.QueryRow(ctx,
		`SELECT id FROM (
			SELECT DISTINCT ON (code) id, code, is_active
			FROM plans WHERE code='trial_3d'
			ORDER BY code, valid_from DESC
		) latest WHERE latest.is_active`).Scan(&planID); err != nil {
		return false, 0, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, valid_until)
		 VALUES ($1,$2,'trial_3d',0, now() + make_interval(days => $3))`, userID, planID, days); err != nil {
		return false, 0, err
	}
	return true, days, nil
}

func GrantTrial(ctx context.Context, pg *pgxpool.Pool, userID string, tgID int64, fingerprint string) (bool, error) {
	tx, err := pg.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	granted, _, err := grantTrialTx(ctx, tx, userID, tgID, fingerprint)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return granted, nil
}
