// Package ratelimit — Go rate limits (см. 03-nonfunctional/02, 04-api-spec.md, V31).
// nginx — первый рубеж; этот middleware — второй (защита при прямом доступе к :8080).
// Фиксированные окна в Redis: INCR + EXPIREAT атомарно через Lua.
// Лимиты (единый источник — 04-api-spec.md):
//
//	POST /v1/auth/* — 20/мин/IP (+anon 20/час/IP уже в auth);
//	POST /v1/readings — 10/мин/user; GET /v1/spreads — 60/мин/IP;
//	/v1/admin/* — 30/мин/admin_id (IP за SSH всегда 127.0.0.1, см. аудит B).
package ratelimit

import (
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"

	"taro/api/internal/apierr"
	"taro/api/internal/auth"
)

const windowLua = `
local n = redis.call('INCR', KEYS[1])
if n == 1 then redis.call('EXPIRE', KEYS[1], ARGV[2]) end
if n > tonumber(ARGV[1]) then return 0 end
return 1
`

// Rule — лимит: window секунд, max запросов.
type Rule struct {
	Window int
	Max    int
}

// Limiter — middleware с правилами по префиксу пути.
type Limiter struct {
	rd    *redis.Client
	rules []struct {
		prefix string
		byUser bool
		// failClosed: при ошибке Redis — 503 (auth/readings/admin), иначе пропуск (spreads).
		failClosed bool
		rule       Rule
	}
}

// New возвращает лимитер с правилами из API-spec.
func New(rd *redis.Client) *Limiter {
	l := &Limiter{rd: rd}
	l.rules = []struct {
		prefix string
		byUser bool
		// failClosed: при ошибке Redis — 503 (auth/readings/admin), иначе пропуск (spreads).
		failClosed bool
		rule       Rule
	}{
		{"/v1/auth/", false, true, Rule{60, 20}},
		{"/v1/readings", true, true, Rule{60, 10}},
		{"/v1/spreads", false, false, Rule{60, 60}},
		{"/v1/admin/", true, true, Rule{60, 30}},
		{"/v1/share", false, false, Rule{60, 30}},
		{"/v1/referral/", true, true, Rule{60, 10}},
		{"/v1/payments/", true, true, Rule{60, 10}},
		{"/v1/diary", true, true, Rule{60, 30}},
		{"/v1/push/", true, true, Rule{60, 20}},
		{"/v1/me", true, true, Rule{60, 10}},
	}
	return l
}

// keyPart — IP (X-Real-IP от nginx) или user_id (taro_jwt ИЛИ taro_admin, см. D1).
func keyPart(r *http.Request, byUser bool) string {
	if byUser {
		for _, name := range []string{auth.CookieName, "taro_admin"} {
			if c, err := r.Cookie(name); err == nil {
				if uid, err := auth.ParseJWT(c.Value); err == nil && uid != "" {
					return "u:" + uid
				}
			}
		}
	}
	ip := strings.TrimSpace(r.Header.Get("X-Real-IP"))
	if ip == "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err == nil {
			ip = host
		} else if parsed := net.ParseIP(strings.Trim(r.RemoteAddr, "[]")); parsed != nil {
			ip = parsed.String()
		}
	}
	if parsed := net.ParseIP(ip); parsed == nil {
		ip = "unknown"
	} else {
		ip = parsed.String()
	}
	return "ip:" + ip
}

// Middleware проверяет лимит по первому совпавшему префиксу.
// Ошибка Redis: failClosed-правила (auth/readings/admin) — 503, spreads — пропуск.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, rl := range l.rules {
			if !strings.HasPrefix(r.URL.Path, rl.prefix) {
				continue
			}
			key := "rl:" + rl.prefix + ":" + keyPart(r, rl.byUser)
			ok, err := l.rd.Eval(r.Context(), windowLua,
				[]string{key}, rl.rule.Max, rl.rule.Window).Int()
			if err != nil {
				if rl.failClosed {
					apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			if ok == 0 {
				w.Header().Set("Retry-After", strconv.Itoa(rl.rule.Window))
				apierr.Write(w, http.StatusTooManyRequests, apierr.CodeRateLimited, "Слишком часто, попробуй позже")
				return
			}
			break
		}
		next.ServeHTTP(w, r)
	})
}
