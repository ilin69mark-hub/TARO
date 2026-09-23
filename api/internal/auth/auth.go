// Package auth — Telegram ID + UUID anon (см. docs/project-book/02-functional/02).
// JWT cookie taro_jwt: HttpOnly; Secure; SameSite=None + CSRF-header; Path=/; 30д.
// Trial 3д выдается 1 раз на связку tg_id+fingerprint (см. T09, 02-functional/05).
package auth

import (
	"context"
	"crypto/hmac"
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
// Возвращает tg_id. Протухший auth_date (>24ч) — ошибка.
func VerifyInitData(initData, botToken string) (int64, error) {
	q, err := url.ParseQuery(initData)
	if err != nil {
		return 0, fmt.Errorf("bad initData: %w", err)
	}
	gotHash := q.Get("hash")
	if gotHash == "" {
		return 0, fmt.Errorf("missing hash")
	}
	// auth_date свежесть
	if ts := q.Get("auth_date"); ts != "" {
		if unix, err := strconv.ParseInt(ts, 10, 64); err == nil {
			if time.Since(time.Unix(unix, 0)) > 24*time.Hour {
				return 0, fmt.Errorf("stale auth_date")
			}
		}
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
	if hex.EncodeToString(mac.Sum(nil)) != gotHash {
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

// IssueJWT выпускает токен с sub=user_id.
func IssueJWT(userID string, ttl time.Duration) (string, error) {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		return "", fmt.Errorf("JWT_SECRET обязателен")
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"exp": time.Now().Add(ttl).Unix(),
		"iat": time.Now().Unix(),
	})
	return tok.SignedString([]byte(secret))
}

// ParseJWT проверяет токен, возвращает user_id.
func ParseJWT(token string) (string, error) {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		return "", fmt.Errorf("JWT_SECRET обязателен")
	}
	tok, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("bad alg")
		}
		return []byte(secret), nil
	})
	if err != nil || !tok.Valid {
		return "", fmt.Errorf("bad token")
	}
	sub, _ := tok.Claims.(jwt.MapClaims)["sub"].(string)
	if sub == "" {
		return "", fmt.Errorf("no sub")
	}
	return sub, nil
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

// telegramRequest — POST /v1/auth/telegram {initData, fingerprint?}.
func (s *Service) HandleTelegram(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InitData    string `json:"initData"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.InitData == "" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректный initData")
		return
	}
	id, isNew, trialDays, err := s.TelegramLogin(r.Context(), req.InitData, req.Fingerprint)
	if err != nil {
		apierr.Write(w, http.StatusUnauthorized, apierr.CodeInvalidTg, "Не удалось подтвердить Telegram")
		return
	}
	tok, err := IssueJWT(id, UserTTL)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать сессию")
		return
	}
	if err := s.rememberSession(r.Context(), id, UserTTL); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать сессию")
		return
	}
	writeCookie(w, tok, UserTTL)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"user_id": id, "is_new": isNew, "trial_days": trialDays})
}

// anonRequest — POST /v1/auth/anon {uuid, fingerprint?}. IP — из X-Real-IP (ставит nginx).
func (s *Service) HandleAnon(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UUID        string `json:"uuid"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UUID == "" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректный uuid")
		return
	}
	ip := r.Header.Get("X-Real-IP")
	if ip == "" {
		ip = "unknown"
	}
	id, err := s.AnonLogin(r.Context(), req.UUID, req.Fingerprint, ip)
	if err != nil {
		if err.Error() == "rate_limited" {
			apierr.Write(w, http.StatusTooManyRequests, apierr.CodeRateLimited, "Слишком много регистраций, попробуй позже")
			return
		}
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать сессию")
		return
	}
	tok, err := IssueJWT(id, UserTTL)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать сессию")
		return
	}
	if err := s.rememberSession(r.Context(), id, UserTTL); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать сессию")
		return
	}
	writeCookie(w, tok, UserTTL)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"user_id": id})
}

// TelegramLogin: вход/регистрация по initData. Новому TG-юзерам — trial 3д (1 раз).
// Возвращает user_id, is_new, trial_days(0|3).
func (s *Service) TelegramLogin(ctx context.Context, initData, fingerprint string) (string, bool, int, error) {
	tgID, err := VerifyInitData(initData, os.Getenv("TG_BOT_TOKEN"))
	if err != nil {
		return "", false, 0, err
	}
	var id string
	var isNew bool
	err = s.pg.QueryRow(ctx, `SELECT id FROM users WHERE tg_id=$1`, tgID).Scan(&id)
	if err == pgx.ErrNoRows {
		isNew = true
		if err := s.pg.QueryRow(ctx,
			`INSERT INTO users (tg_id, fingerprint) VALUES ($1,$2) RETURNING id`, tgID, fingerprint).Scan(&id); err != nil {
			return "", false, 0, err
		}
	} else if err != nil {
		return "", false, 0, err
	}
	trialDays := 0
	if isNew {
		if granted, err := GrantTrial(ctx, s.pg, id, tgID, fingerprint); err != nil {
			return "", false, 0, err
		} else if granted {
			trialDays = 3
		}
	}
	return id, isNew, trialDays, nil
}

// AnonLogin: вход/создание по uuid из localStorage. Лимит 20 reg/час/IP.
func (s *Service) AnonLogin(ctx context.Context, uuid, fingerprint, ip string) (string, error) {
	var id string
	err := s.pg.QueryRow(ctx, `SELECT id FROM users WHERE anon_uuid=$1`, uuid).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != pgx.ErrNoRows {
		return "", err
	}
	key := "rl:reg:" + ip
	n, err := s.rd.Incr(ctx, key).Result()
	if err != nil {
		return "", err
	}
	if n == 1 {
		_ = s.rd.Expire(ctx, key, time.Hour).Err()
	}
	if n > AnonRegPerIPPerHour {
		return "", fmt.Errorf("rate_limited")
	}
	if err := s.pg.QueryRow(ctx,
		`INSERT INTO users (anon_uuid, fingerprint) VALUES ($1,$2) RETURNING id`, uuid, fingerprint).Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// GrantTrial выдает trial_3d 1 раз на связку tg_id+fingerprint.
// Возвращает granted. Идемпотентен: повторный вызов — false без дубля.
func GrantTrial(ctx context.Context, pg *pgxpool.Pool, userID string, tgID int64, fingerprint string) (bool, error) {
	// trial уже был у этого юзера?
	var n int
	if err := pg.QueryRow(ctx,
		`SELECT COUNT(*) FROM subscriptions WHERE user_id=$1 AND plan_code='trial_3d'`, userID).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	var planID string
	if err := pg.QueryRow(ctx,
		`SELECT id FROM plans WHERE code='trial_3d' AND is_active ORDER BY valid_from DESC LIMIT 1`).Scan(&planID); err != nil {
		return false, err
	}
	_, err := pg.Exec(ctx,
		`INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, valid_until)
		 VALUES ($1,$2,'trial_3d',0, now() + interval '3 days')`, userID, planID)
	if err != nil {
		return false, err
	}
	return true, nil
}
