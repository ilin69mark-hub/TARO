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
	"context"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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

// Бюджеты ожидания Redis на горячем пути (A18/F-42). До правки один запрос при
// мёртвом Redis платил ~1.7 с (дефолтные повторы go-redis плюс DialTimeout),
// и это повторялось на каждом запросе: fail-open маршрут «просто пропускал»
// клиента, но всё равно держал соединение и горутину.
const (
	// failOpenBudget — сколько ждём Redis на fail-open маршрутах. Не 50 мс «на
	// глаз»: этого хватает на один сетевой round-trip внутри контура, а любой
	// дефект сети превращается в промах по кэшу лимита, а не в очередь.
	failOpenBudget = 50 * time.Millisecond
	// failClosedBudget — короче: здесь Redis обязателен, и медленный ответ
	// означает, что отвечать нечем (503), ждать точнее — незачем.
	failClosedBudget = 30 * time.Millisecond
	// memoryFallbackMax — предел числа ключей в памяти. Сбойный Redis не
	// должен превращать процесс в утечку: ключи выexpire'иваются, а при
	// переполнении вытесняются самые старые.
	memoryFallbackMax = 8192
	// memorySweepAt — порог, с которого начинается уборка просроченных ключей.
	// Амортизированно: уборка не выполняется на каждом запросе.
	memorySweepAt = memoryFallbackMax * 3 / 4
	// memoryFallbackSlack — сколько ключей сверх предела разрешено иметь до
	// уборки. Без него каждая вставка сверх предела запускала бы сортировку
	// всей карты (упёршись в 8192 заливок — сортировка 8192 элементов на КАЖДЫЙ
	// запрос при мёртвом Redis). С запасом уборка идёт пачками.
	memoryFallbackSlack = memoryFallbackMax / 8
)

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
	// mem — счётчик в памяти для fail-open маршрутов, когда Redis недоступен
	// (A18/F-42). Без него «быстрый fail-open» означал бы «лимита нет».
	mem *memoryCounter
}

// memoryEntry — одно окно в памяти.
type memoryEntry struct {
	count     int
	expiresAt time.Time
}

// memoryCounter — счётчик окон в памяти: те же ключи и то же окно, что в Redis,
// но в пределах одного процесса. Границы: ключи выexpire'иваются, число ключей
// ограничено, при переполнении вытесняются самые старые.
type memoryCounter struct {
	mu      sync.Mutex
	entries map[string]memoryEntry
	max     int
	slack   int
	// opsSinceSweep — счётчик операций до следующей уборки.
	opsSinceSweep int
	// lastSweep — время последней уборки. Уборка по счётчику операций не
	// срабатывает при редком трафике, и просроченные окна жили бы в памяти
	// минутами после истечения. Проверка времени дешёвая, поэтому уборка идёт
	// не реже раза в секунду независимо от нагрузки.
	lastSweep time.Time
}

func newMemoryCounter(max int) *memoryCounter {
	return &memoryCounter{entries: make(map[string]memoryEntry), max: max, slack: max / 8}
}

// allow — эквивалент windowLua в памяти: true, если запрос в пределах Max.
func (c *memoryCounter) allow(key string, window, max int) bool {
	if c == nil {
		return true
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.opsSinceSweep++
	if c.opsSinceSweep >= memorySweepAt || len(c.entries) > c.max+c.slack || now.Sub(c.lastSweep) >= time.Second {
		c.sweepLocked(now)
		c.opsSinceSweep = 0
		c.lastSweep = now
	}
	entry, ok := c.entries[key]
	if !ok || now.After(entry.expiresAt) {
		entry = memoryEntry{expiresAt: now.Add(time.Duration(window) * time.Second)}
	}
	entry.count++
	c.entries[key] = entry
	return entry.count <= max
}

// sweepLocked — выбросить просроченные, а если всё ещё много — самые старые.
func (c *memoryCounter) sweepLocked(now time.Time) {
	for key, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
	if len(c.entries) <= c.max {
		return
	}
	type aged struct {
		key string
		at  time.Time
	}
	list := make([]aged, 0, len(c.entries))
	for key, entry := range c.entries {
		list = append(list, aged{key: key, at: entry.expiresAt})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].at.Before(list[j].at) })
	for i := 0; i < len(list)-c.max; i++ {
		delete(c.entries, list[i].key)
	}
}

func (c *memoryCounter) size() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// New возвращает лимитер с правилами из API-spec.
func New(rd *redis.Client) *Limiter {
	l := &Limiter{rd: rd, mem: newMemoryCounter(memoryFallbackMax)}
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
//
// A18/F-42: обращение к Redis ограничено бюджетом времени (failOpenBudget /
// failClosedBudget). Ошибка или таймаут:
//   - failClosed (auth/readings/admin) — 503, без ожидания;
//   - fail-open (spreads/share) — запрос проходит, но лимит продолжает
//     считаться в памяти, чтобы «мягкая деградация» не была «снятием лимита».
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, rl := range l.rules {
			if !strings.HasPrefix(r.URL.Path, rl.prefix) {
				continue
			}
			key := "rl:" + rl.prefix + ":" + keyPart(r, rl.byUser)
			budget := failOpenBudget
			if rl.failClosed {
				budget = failClosedBudget
			}
			ok, err := l.eval(r, key, budget, rl.rule)
			if err != nil {
				if rl.failClosed {
					apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
					return
				}
				// fail-open: Redis не ответил в бюджете — считаем локально.
				ok = l.mem.allow(key, rl.rule.Window, rl.rule.Max)
			}
			if !ok {
				w.Header().Set("Retry-After", strconv.Itoa(rl.rule.Window))
				apierr.Write(w, http.StatusTooManyRequests, apierr.CodeRateLimited, "Слишком часто, попробуй позже")
				return
			}
			break
		}
		next.ServeHTTP(w, r)
	})
}

// eval — одна попытка INCR+EXPIRE в пределах бюджета.
func (l *Limiter) eval(r *http.Request, key string, budget time.Duration, rule Rule) (bool, error) {
	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()
	n, err := l.rd.Eval(ctx, windowLua, []string{key}, rule.Max, rule.Window).Int()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}
