// Package admin — кабинет настроек на :8081 (см. docs/project-book/02-functional/07, D1).
// Auth: taro_admin JWT 12ч (HttpOnly/Secure/SameSite=Strict) + role=admin в БД.
// Login: POST /v1/admin/login {initData} — TG-подпись + (role admin ИЛИ tg_id в ADMIN_TG_IDS).
// Publish применяет diff + пишет admin_audit (см. D1).
package admin

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"taro/api/internal/apierr"
	"taro/api/internal/auth"
	"taro/api/internal/spreads"
)

// isLoopback — RemoteAddr с loopback (cron-токен только локально, см. S10).
func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// PlansCacheKey — см. 04-architecture/05-cache-redis.md.
const PlansCacheKey = "plans:active:v1"

// AdminCookie — имя admin-JWT (см. D1).
const AdminCookie = "taro_admin"

// AdminTTL — 12 часов (см. 04-api-spec.md).
const AdminTTL = 12 * time.Hour

// Service обслуживает кабинет.
type Service struct {
	pg *pgxpool.Pool
	rd *redis.Client
}

// New возвращает админ-сервис.
func New(pg *pgxpool.Pool, rd *redis.Client) *Service {
	return &Service{pg: pg, rd: rd}
}

// adminCtxKey — admin user_id в контексте.
type adminCtxKey struct{}

// AdminID достает admin user_id из контекста.
func AdminID(ctx context.Context) string {
	uid, _ := ctx.Value(adminCtxKey{}).(string)
	return uid
}

// isAdmin: role=admin в БД ИЛИ tg_id в ADMIN_TG_IDS (первичная выдача).
func (s *Service) isAdmin(ctx context.Context, userID string, tgID int64) bool {
	var role string
	if err := s.pg.QueryRow(ctx, `SELECT role FROM users WHERE id=$1`, userID).Scan(&role); err == nil && role == "admin" {
		return true
	}
	for _, part := range strings.Split(os.Getenv("ADMIN_TG_IDS"), ",") {
		if id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64); err == nil && id == tgID && tgID != 0 {
			return true
		}
	}
	return false
}

// HandleLogin — POST /v1/admin/login {initData}: TG + admin → taro_admin cookie.
func (s *Service) HandleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InitData string `json:"initData"`
	}
	if !apierr.Decode(w, r, &req) || req.InitData == "" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректный initData")
		return
	}
	tgID, err := auth.VerifyInitData(req.InitData, os.Getenv("TG_BOT_TOKEN"))
	if err != nil {
		apierr.Write(w, http.StatusUnauthorized, apierr.CodeInvalidTg, "Не удалось подтвердить Telegram")
		return
	}
	ctx := r.Context()
	var userID string
	err = s.pg.QueryRow(ctx, `SELECT id FROM users WHERE tg_id=$1`, tgID).Scan(&userID)
	if err != nil {
		// создаем юзера, роль проверит whitelist
		if err := s.pg.QueryRow(ctx,
			`INSERT INTO users (tg_id, role) VALUES ($1,'user') RETURNING id`, tgID).Scan(&userID); err != nil {
			apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать")
			return
		}
	}
	if !s.isAdmin(ctx, userID, tgID) {
		apierr.Write(w, http.StatusForbidden, apierr.CodeForbidden, "Нет доступа")
		return
	}
	// whitelist-вход повышает роль (иначе RequireAdmin отвергнет, см. D1)
	_, _ = s.pg.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1 AND role != 'admin'`, userID)
	tok, err := auth.IssueJWT("admin:"+userID, AdminTTL)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать сессию")
		return
	}
	if err := s.rd.Set(ctx, "sess:admin:"+userID, "1", AdminTTL).Err(); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать сессию")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: AdminCookie, Value: tok, Path: "/", MaxAge: int(AdminTTL.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// RequireAdmin — middleware: taro_admin JWT (sub=admin:<uid>) + живой sess:admin + роль.
// S10: либо X-Admin-Token == ADMIN_API_TOKEN (cron/server-to-server, ТОЛЬКО с loopback).
func (s *Service) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tok := os.Getenv("ADMIN_API_TOKEN"); tok != "" && r.Header.Get("X-Admin-Token") == tok {
			if !isLoopback(r.RemoteAddr) {
				apierr.Write(w, http.StatusForbidden, apierr.CodeForbidden, "Нет доступа")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminCtxKey{}, "cron")))
			return
		}
		c, err := r.Cookie(AdminCookie)
		if err != nil {
			apierr.Write(w, http.StatusForbidden, apierr.CodeForbidden, "Нет доступа")
			return
		}
		sub, err := auth.ParseJWT(c.Value)
		if err != nil || !strings.HasPrefix(sub, "admin:") {
			apierr.Write(w, http.StatusForbidden, apierr.CodeForbidden, "Нет доступа")
			return
		}
		uid := strings.TrimPrefix(sub, "admin:")
		if n, err := s.rd.Exists(r.Context(), "sess:admin:"+uid).Result(); err != nil || n == 0 {
			apierr.Write(w, http.StatusForbidden, apierr.CodeForbidden, "Сессия завершена")
			return
		}
		var role string
		if err := s.pg.QueryRow(r.Context(), `SELECT role FROM users WHERE id=$1`, uid).Scan(&role); err != nil || role != "admin" {
			apierr.Write(w, http.StatusForbidden, apierr.CodeForbidden, "Нет доступа")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminCtxKey{}, uid)))
	})
}

// HandleGetConfig — GET /v1/admin/config: app_config + plans + spreads (источник — PG).
func (s *Service) HandleGetConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	config := map[string]json.RawMessage{}
	rows, err := s.pg.Query(ctx, `SELECT key, value FROM app_config`)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось прочитать конфиг")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var v json.RawMessage
		if err := rows.Scan(&k, &v); err != nil {
			apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось прочитать конфиг")
			return
		}
		config[k] = v
	}
	if err := rows.Err(); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось прочитать конфиг")
		return
	}
	type plan struct {
		Code     string `json:"code"`
		Price    int    `json:"price_rub"`
		Stars    int    `json:"stars_amount"`
		Duration *int   `json:"duration_days"`
		Active   bool   `json:"is_active"`
	}
	plans := []plan{}
	prows, err := s.pg.Query(ctx,
		`SELECT DISTINCT ON (code) code, price_rub, stars_amount, duration_days, is_active FROM plans ORDER BY code, valid_from DESC`)
	if err == nil {
		defer prows.Close()
		for prows.Next() {
			var p plan
			if err := prows.Scan(&p.Code, &p.Price, &p.Stars, &p.Duration, &p.Active); err == nil {
				plans = append(plans, p)
			}
		}
	}
	type spread struct {
		Code    string `json:"code"`
		Active  bool   `json:"is_active"`
		Sort    int    `json:"sort_order"`
		Premium bool   `json:"is_premium"`
	}
	spreadsList := []spread{}
	srows, err := s.pg.Query(ctx,
		`SELECT code, is_active, sort_order, is_premium FROM spreads ORDER BY sort_order`)
	if err == nil {
		defer srows.Close()
		for srows.Next() {
			var sp spread
			if err := srows.Scan(&sp.Code, &sp.Active, &sp.Sort, &sp.Premium); err == nil {
				spreadsList = append(spreadsList, sp)
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"app_config": config, "plans": plans, "spreads": spreadsList,
	})
}

// allowedConfigKeys — allowlist ключей app_config (неизвестные → 422, см. D1).
var allowedConfigKeys = map[string]bool{
	"free.daily_limit": true, "love.free_weekly": true, "history.free_limit": true,
	"trial": true, "referral": true, "copy.paywall_title": true, "copy.paywall_desc": true,
	"copy.paywall_cta": true, "ai": true, "ab.price_month": true, "offers.winback": true,
	"spreads.seasonal": true, "payments.yookassa": true,
}

type planDiff struct {
	Code     string `json:"code"`
	PriceRub *int   `json:"price_rub"`
	Stars    *int   `json:"stars_amount"`
	Duration *int   `json:"duration_days"`
	Active   *bool  `json:"is_active"`
}

type spreadDiff struct {
	Code      string `json:"code"`
	Active    *bool  `json:"is_active"`
	SortOrder *int   `json:"sort_order"`
	Premium   *bool  `json:"is_premium"`
}

type publishRequest struct {
	AppConfig map[string]json.RawMessage `json:"app_config"`
	Plans     []planDiff                 `json:"plans"`
	Spreads   []spreadDiff               `json:"spreads"`
}

// HandlePublish — POST /v1/admin/config/publish: применяет diff в транзакции +
// пишет admin_audit + точечно инвалидирует кэш (<5с SLA).
func (s *Service) HandlePublish(w http.ResponseWriter, r *http.Request) {
	var req publishRequest
	if !apierr.Decode(w, r, &req) {
		return
	}
	ctx := r.Context()
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось начать")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	applied := map[string]int{"app_config": 0, "plans": 0, "spreads": 0}
	for k, v := range req.AppConfig {
		if !allowedConfigKeys[k] {
			apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Неизвестный ключ: "+k)
			return
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO app_config (key, value, updated_at) VALUES ($1,$2,now())
			ON CONFLICT (key) DO UPDATE SET value=$2, updated_at=now()`, k, v); err != nil {
			apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось применить")
			return
		}
		applied["app_config"]++
	}
	for _, p := range req.Plans {
		var cur struct {
			price, stars int
			dur          *int
			active       bool
		}
		if err := tx.QueryRow(ctx, `
			SELECT price_rub, stars_amount, duration_days, is_active FROM plans
			 WHERE code=$1 ORDER BY valid_from DESC LIMIT 1`, p.Code).Scan(&cur.price, &cur.stars, &cur.dur, &cur.active); err != nil {
			apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Неизвестный тариф: "+p.Code)
			return
		}
		if p.PriceRub != nil {
			if *p.PriceRub <= 0 || *p.PriceRub > 100000 {
				apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректная цена: "+p.Code)
				return
			}
			cur.price = *p.PriceRub
		}
		if p.Stars != nil {
			if *p.Stars <= 0 || *p.Stars > 100000 {
				apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректные stars: "+p.Code)
				return
			}
			cur.stars = *p.Stars
		}
		if p.Duration != nil {
			if *p.Duration <= 0 || *p.Duration > 3650 {
				apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректный срок: "+p.Code)
				return
			}
			cur.dur = p.Duration
		}
		if p.Active != nil {
			cur.active = *p.Active
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO plans (code, price_rub, stars_amount, duration_days, is_active, valid_from)
			VALUES ($1,$2,$3,$4,$5,now())`, p.Code, cur.price, cur.stars, cur.dur, cur.active); err != nil {
			apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось применить")
			return
		}
		applied["plans"]++
	}
	for _, sp := range req.Spreads {
		sets := []string{}
		args := []any{sp.Code}
		// NB: номер плейсхолдера = len(args) ПОСЛЕ append (см. D1 off-by-one).
		if sp.Active != nil {
			args = append(args, *sp.Active)
			sets = append(sets, "is_active=$"+itoa(len(args)))
		}
		if sp.SortOrder != nil {
			args = append(args, *sp.SortOrder)
			sets = append(sets, "sort_order=$"+itoa(len(args)))
		}
		if sp.Premium != nil {
			args = append(args, *sp.Premium)
			sets = append(sets, "is_premium=$"+itoa(len(args)))
		}
		if len(sets) == 0 {
			continue
		}
		res, err := tx.Exec(ctx,
			`UPDATE spreads SET `+strings.Join(sets, ", ")+` WHERE code=$1`, args...)
		if err != nil || res.RowsAffected() == 0 {
			apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Неизвестный расклад: "+sp.Code)
			return
		}
		applied["spreads"]++
	}
	diffJSON, _ := json.Marshal(req)
	if _, err := tx.Exec(ctx,
		`INSERT INTO admin_audit (admin_id, action, diff) VALUES (NULLIF($1,'cron')::uuid,'config:publish',$2)`,
		AdminID(ctx), diffJSON); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось записать аудит")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось зафиксировать")
		return
	}
	deleted := 0
	for _, key := range []string{spreads.CacheKey, PlansCacheKey} {
		if n, err := s.rd.Del(ctx, key).Result(); err == nil {
			deleted += int(n)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "applied": applied, "invalidated": deleted})
}

func itoa(n int) string { return strconv.Itoa(n) }

// HandleRotateSeasonal — POST /v1/admin/rotate-seasonal (см. V22).
// + audit записи (см. D1).
func (s *Service) HandleRotateSeasonal(w http.ResponseWriter, r *http.Request) {
	var raw json.RawMessage
	if err := s.pg.QueryRow(r.Context(),
		`SELECT value FROM app_config WHERE key='spreads.seasonal'`).Scan(&raw); err != nil {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нет конфига spreads.seasonal")
		return
	}
	var windows []struct {
		Code string `json:"code"`
		From string `json:"from"`
		To   string `json:"to"`
	}
	if json.Unmarshal(raw, &windows) != nil || len(windows) == 0 {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Пустой конфиг")
		return
	}
	today := time.Now().Format("2006-01-02")
	changed := 0
	for _, win := range windows {
		want := win.From <= today && today <= win.To
		res, err := s.pg.Exec(r.Context(),
			`UPDATE spreads SET is_active=$1 WHERE code=$2 AND is_active IS DISTINCT FROM $1`, want, win.Code)
		if err != nil {
			continue
		}
		changed += int(res.RowsAffected())
	}
	ctx := r.Context()
	_, _ = s.pg.Exec(ctx,
		`INSERT INTO admin_audit (admin_id, action, diff) VALUES (NULLIF($1,'cron')::uuid,'rotate-seasonal',$2)`,
		AdminID(ctx), raw)
	_ = s.rd.Del(ctx, spreads.CacheKey).Err()
	_ = s.rd.Del(ctx, PlansCacheKey).Err()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "changed": changed})
}
