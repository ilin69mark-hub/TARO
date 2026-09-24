// Package admin — кабинет настроек на :8081 (см. docs/project-book/02-functional/07, D1).
// Auth: taro_admin JWT 12ч (HttpOnly/Secure/SameSite=Strict) + role=admin в БД.
// Login: POST /v1/admin/login {initData} — TG-подпись + (role admin ИЛИ tg_id в ADMIN_TG_IDS).
// Publish применяет diff + пишет admin_audit (см. D1).
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
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

// isLocalOrigin — Origin/Referer указывает на локальный SSH-туннель (аудит D).
// Админка живёт только на 127.0.0.1:8081, любой внешний Origin — чужой сайт.
func isLocalOrigin(r *http.Request, origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	host := u.Hostname()
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

// whitelistedTgID — tg_id в ADMIN_TG_IDS (первичная выдача админа).
func whitelistedTgID(tgID int64) bool {
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
	var userID, role string
	created := false
	err = s.pg.QueryRow(ctx, `SELECT id, role FROM users WHERE tg_id=$1`, tgID).Scan(&userID, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		if !whitelistedTgID(tgID) {
			apierr.Write(w, http.StatusForbidden, apierr.CodeForbidden, "Нет доступа")
			return
		}
		if err := s.pg.QueryRow(ctx,
			`INSERT INTO users (tg_id, role) VALUES ($1,'user') RETURNING id, role`, tgID).Scan(&userID, &role); err != nil {
			apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать")
			return
		}
		created = true
	} else if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось проверить доступ")
		return
	}
	if role != "admin" {
		if !created {
			apierr.Write(w, http.StatusForbidden, apierr.CodeForbidden, "Нет доступа")
			return
		}
		if _, err := s.pg.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1 AND role != 'admin'`, userID); err != nil {
			apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать")
			return
		}
	}
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

// HandleLogout — POST /v1/admin/logout: отзыв admin-сессии (аудит B: угона без отзыва нет).
func (s *Service) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(AdminCookie); err == nil {
		if sub, err := auth.ParseJWT(c.Value); err == nil {
			uid := strings.TrimPrefix(sub, "admin:")
			_, _ = s.rd.Del(r.Context(), "sess:admin:"+uid).Result()
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: AdminCookie, Value: "", Path: "/", MaxAge: -1,
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
		// Аудит D: Origin-gate для cookie-ветки (браузер всегда шлёт Origin на POST;
		// curl без Origin пропускаем — иначе ломаем скрипты владельца).
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			origin := r.Header.Get("Origin")
			if origin == "" {
				origin = r.Header.Get("Referer")
			}
			if origin != "" && !isLocalOrigin(r, origin) {
				apierr.Write(w, http.StatusForbidden, apierr.CodeForbidden, "Неверный Origin")
				return
			}
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
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось прочитать конфиг")
		return
	}
	defer prows.Close()
	for prows.Next() {
		var p plan
		if err := prows.Scan(&p.Code, &p.Price, &p.Stars, &p.Duration, &p.Active); err != nil {
			apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось прочитать конфиг")
			return
		}
		plans = append(plans, p)
	}
	if err := prows.Err(); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось прочитать конфиг")
		return
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
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось прочитать конфиг")
		return
	}
	defer srows.Close()
	for srows.Next() {
		var sp spread
		if err := srows.Scan(&sp.Code, &sp.Active, &sp.Sort, &sp.Premium); err != nil {
			apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось прочитать конфиг")
			return
		}
		spreadsList = append(spreadsList, sp)
	}
	if err := srows.Err(); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось прочитать конфиг")
		return
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

func configObject(v json.RawMessage, allowed ...string) (map[string]json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(v, &m) != nil || m == nil {
		return nil, false
	}
	keys := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		keys[key] = struct{}{}
	}
	for key := range m {
		if _, ok := keys[key]; !ok {
			return nil, false
		}
	}
	return m, true
}

func configInt(v json.RawMessage, min, max int) bool {
	if strings.TrimSpace(string(v)) == "null" {
		return false
	}
	var n int
	if json.Unmarshal(v, &n) == nil {
		return n >= min && n <= max
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	return err == nil && n >= min && n <= max
}

func configFloat(v json.RawMessage, min, max float64) bool {
	if strings.TrimSpace(string(v)) == "null" {
		return false
	}
	var n float64
	return json.Unmarshal(v, &n) == nil && n >= min && n <= max
}

func configBool(v json.RawMessage) bool {
	if strings.TrimSpace(string(v)) == "null" {
		return false
	}
	var b bool
	return json.Unmarshal(v, &b) == nil
}

func configString(v json.RawMessage, max int) bool {
	var s string
	if json.Unmarshal(v, &s) != nil || s == "" || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return false
		}
	}
	return true
}

func validateConfigValue(k string, v json.RawMessage) bool {
	if len(v) == 0 || len(v) > 8192 || strings.Contains(string(v), "\x00") {
		return false
	}
	switch k {
	case "free.daily_limit":
		return configInt(v, 0, 100)
	case "love.free_weekly":
		return configInt(v, 0, 100)
	case "history.free_limit":
		return configInt(v, 1, 500)
	case "ai":
		m, ok := configObject(v, "model", "fallback", "max_tokens", "temperature", "monthly_calls")
		if !ok {
			return false
		}
		if value, exists := m["model"]; exists && !configString(value, 200) {
			return false
		}
		if value, exists := m["fallback"]; exists && !configString(value, 200) {
			return false
		}
		if value, exists := m["max_tokens"]; exists && !configInt(value, 1, 100000) {
			return false
		}
		if value, exists := m["temperature"]; exists && !configFloat(value, 0, 2) {
			return false
		}
		if value, exists := m["monthly_calls"]; exists && !configInt(value, 0, 10000000) {
			return false
		}
		return true
	case "ab.price_month":
		m, ok := configObject(v, "enabled", "control", "test", "split", "pct")
		if !ok {
			return false
		}
		if value, exists := m["enabled"]; exists && !configBool(value) {
			return false
		}
		if value, exists := m["control"]; exists && !configInt(value, 1, 100000) {
			return false
		}
		if value, exists := m["test"]; exists && !configInt(value, 1, 100000) {
			return false
		}
		if value, exists := m["split"]; exists && !configFloat(value, 0, 100) {
			return false
		}
		if value, exists := m["pct"]; exists && !configFloat(value, 0, 90) {
			return false
		}
		return true
	case "offers.winback":
		m, ok := configObject(v, "enabled", "pct")
		if !ok {
			return false
		}
		if value, exists := m["enabled"]; exists && !configBool(value) {
			return false
		}
		if value, exists := m["pct"]; exists && !configFloat(value, 0, 90) {
			return false
		}
		return true
	case "trial":
		m, ok := configObject(v, "enabled", "days", "require_tg")
		if !ok {
			return false
		}
		if value, exists := m["enabled"]; exists && !configBool(value) {
			return false
		}
		if value, exists := m["days"]; exists && !configInt(value, 1, 365) {
			return false
		}
		if value, exists := m["require_tg"]; exists && !configBool(value) {
			return false
		}
		return true
	case "referral":
		m, ok := configObject(v, "enabled", "bonus_days", "monthly_cap")
		if !ok {
			return false
		}
		if value, exists := m["enabled"]; exists && !configBool(value) {
			return false
		}
		if value, exists := m["bonus_days"]; exists && !configInt(value, 1, 365) {
			return false
		}
		if value, exists := m["monthly_cap"]; exists && !configInt(value, 1, 10000) {
			return false
		}
		return true
	case "spreads.seasonal":
		var windows []struct {
			Code string `json:"code"`
			From string `json:"from"`
			To   string `json:"to"`
		}
		if json.Unmarshal(v, &windows) != nil || len(windows) == 0 || len(windows) > 100 {
			return false
		}
		for _, window := range windows {
			if !configString(json.RawMessage(strconv.Quote(window.Code)), 64) {
				return false
			}
			from, err := time.Parse("2006-01-02", window.From)
			if err != nil {
				return false
			}
			to, err := time.Parse("2006-01-02", window.To)
			if err != nil || from.After(to) {
				return false
			}
		}
		return true
	case "payments.yookassa":
		m, ok := configObject(v, "enabled")
		return ok && (len(m) == 0 || configBool(m["enabled"]))
	case "copy.paywall_title", "copy.paywall_desc", "copy.paywall_cta":
		return configString(v, 500)
	default:
		return false
	}
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
		if !validateConfigValue(k, v) {
			apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректное значение: "+k)
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
		if sp.SortOrder != nil && (*sp.SortOrder < -1000 || *sp.SortOrder > 1000) {
			apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректный sort_order: "+sp.Code)
			return
		}
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
	// Аудит D: даты обязаны парситься, код — существовать (иначе тихая неработающая ротация).
	for _, win := range windows {
		if _, err := time.Parse("2006-01-02", win.From); err != nil {
			apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректный from: "+win.Code)
			return
		}
		if _, err := time.Parse("2006-01-02", win.To); err != nil {
			apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректный to: "+win.Code)
			return
		}
		if win.From > win.To {
			apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "from позже to: "+win.Code)
			return
		}
		var ok bool
		if err := s.pg.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM spreads WHERE code=$1)`, win.Code).Scan(&ok); err != nil || !ok {
			apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Неизвестный расклад: "+win.Code)
			return
		}
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
