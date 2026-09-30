// A18/F-42: при мёртвом Redis каждый fail-open запрос платил ~1.7 с ожидания.
// Тест меряет ВРЕМЯ ответа и поведение лимита, а не только коды:
//   - fail-open маршрут (/v1/spreads, /v1/share) обязан отвечать быстро;
//   - fail-closed маршруты (/v1/auth/*, /v1/readings) — тоже быстро, но 503;
//   - лимит при этом НЕ теряется: при недоступном Redis fail-open продолжает
//     считать запросы (в памяти), иначе «быстрый ответ» = «без лимита».
package ratelimit

import (
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"taro/api/internal/testutil"
)

// deadRedisAddr — порт, который не слушается. 127.0.0.1:1 отвечает
// ECONNREFUSED сразу, но go-redis по умолчанию делает несколько попыток с
// экспоненциальной паузой — именно это и давало 1.7 с.
const deadRedisAddr = "127.0.0.1:1"

func newDeadLimiter(t *testing.T) (*Limiter, *redis.Client) {
	t.Helper()
	// Клиент СПЕЦИАЛЬНО собран «как до A18»: дефолты go-redis без явных
	// таймаутов давали повторы и ~1.7 с на запрос. Проверяем, что лимитер
	// укладывается в бюджет даже с таким клиентом.
	dead := redis.NewClient(&redis.Options{
		Addr:        deadRedisAddr,
		DialTimeout: 2 * time.Second,
	})
	t.Cleanup(func() { _ = dead.Close() })
	return New(dead), dead
}

// TestFailOpenFastWhenRedisDown — DoD A18: недоступный Redis не съедает
// запрос. Верхняя граница времени — главное утверждение теста.
func TestFailOpenFastWhenRedisDown(t *testing.T) {
	l, _ := newDeadLimiter(t)
	h := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	ip := testutil.UniqueIP(t)

	const reqs = 20
	worst := time.Duration(0)
	start := time.Now()
	for i := 0; i < reqs; i++ {
		reqStart := time.Now()
		code := fireAt(h, "/v1/spreads", ip)
		took := time.Since(reqStart)
		if took > worst {
			worst = took
		}
		if code != 200 && code != http.StatusTooManyRequests {
			t.Fatalf("fail-open route: want 200 or 429 got %d", code)
		}
	}
	t.Logf("fail-open: %d sequential requests in %s, worst %s (budget %s per request)",
		reqs, time.Since(start).Round(time.Millisecond), worst.Round(time.Millisecond), failOpenBudget)
	if worst > failOpenBudget*4 {
		t.Fatalf("fail-open request took %s: budget is %s, DoD is < 100ms", worst, failOpenBudget)
	}
}

// TestFailClosedFailsFastWhenRedisDown — 503 без долгого ожидания: падение
// кэша не должно выглядеть как «сервис подвис».
func TestFailClosedFailsFastWhenRedisDown(t *testing.T) {
	l, _ := newDeadLimiter(t)
	h := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	ip := testutil.UniqueIP(t)

	worst := time.Duration(0)
	for _, path := range []string{"/v1/auth/anon", "/v1/readings", "/v1/me"} {
		reqStart := time.Now()
		code := fireAt(h, path, ip)
		if took := time.Since(reqStart); took > worst {
			worst = took
		}
		if code != http.StatusServiceUnavailable {
			t.Fatalf("%s: want 503 got %d", path, code)
		}
	}
	if worst > failOpenBudget*4 {
		t.Fatalf("fail-closed request took %s: budget is %s", worst, failOpenBudget)
	}
}

// TestFailOpenStillLimitsWhenRedisDown — быстрый ответ не должен означать
// «лимита нет». Пока Redis мёртв, счётчик живёт в памяти, и тот же IP упирается
// в Max, как и при живом Redis.
func TestFailOpenStillLimitsWhenRedisDown(t *testing.T) {
	l, _ := newDeadLimiter(t)
	h := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	ip := testutil.UniqueIP(t)

	allowed, limited := 0, 0
	for i := 0; i < 200; i++ {
		switch code := fireAt(h, "/v1/spreads", ip); code {
		case 200:
			allowed++
		case http.StatusTooManyRequests:
			limited++
		default:
			t.Fatalf("unexpected code %d", code)
		}
	}
	if limited == 0 {
		t.Fatalf("fail-open must keep limiting without Redis: allowed=%d limited=%d", allowed, limited)
	}
	if allowed > 60 {
		t.Fatalf("in-memory limiter let %d requests through, rule Max is 60", allowed)
	}
	t.Logf("dead Redis: allowed=%d limited=%d (rule Max=60)", allowed, limited)
}

// TestConcurrentFailOpenDoesNotHoldGoroutines — DoD: 300 конкурентных запросов
// не держат горутины дольше 200 мс. Проверяем и «хвост» (последний запрос),
// и суммарное время: иначе деградация прячется в одном медленном запросе.
func TestConcurrentFailOpenDoesNotHoldGoroutines(t *testing.T) {
	l, _ := newDeadLimiter(t)
	h := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	ip := testutil.UniqueIP(t)

	const concurrency = 300
	var slowest atomic.Int64
	var wg sync.WaitGroup
	wg.Add(concurrency)
	start := time.Now()
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			reqStart := time.Now()
			fireAt(h, "/v1/spreads", ip)
			if took := int64(time.Since(reqStart)); took > slowest.Load() {
				slowest.Store(took)
			}
		}()
	}
	wg.Wait()
	total := time.Since(start)
	t.Logf("dead Redis: %d concurrent requests total %s, slowest %s",
		concurrency, total.Round(time.Millisecond), time.Duration(slowest.Load()).Round(time.Millisecond))
	if slowest.Load() > int64(200*time.Millisecond) {
		t.Fatalf("slowest of %d concurrent requests held for %s, DoD is 200ms",
			concurrency, time.Duration(slowest.Load()))
	}
	// redis-go держит пул соединений: 300 последовательных «мёртвых» попыток по
	// 1.7 с заняли бы минуты, суммарное время это ловит.
	if total > 5*time.Second {
		t.Fatalf("total time for %d concurrent requests is %s: attempts are being retried", concurrency, total)
	}
}

// TestInMemoryLimiterStaysBounded — fallback не должен течь по памяти: сбойный
// Redis не превращает процесс в утечку. Проверяются обе границы: вытеснение при
// переполнении и удаление просроченных окон.
//
// Мутации: отключить уборку (`if false`) и отключить вытеснение по возрасту.
func TestInMemoryLimiterStaysBounded(t *testing.T) {
	l, _ := newDeadLimiter(t)
	counter := l.mem

	// Много РАЗНЫХ ключей: 5000 вызовов с одним и тем же ключом ничего не
	// доказывают о размере карты.
	const keys = 20000
	for i := 0; i < keys; i++ {
		counter.allow("rl:/v1/spreads:ip:10.0."+strconv.Itoa(i/250)+"."+strconv.Itoa(i%250), 60, 60)
	}
	// Предел = max + запас: запас существует, чтобы уборка шла пачками, а не на
	// каждом запросе. Проверять надо именно эту границу, а не выдуманную.
	if limit := memoryFallbackMax + memoryFallbackSlack; counter.size() > limit {
		t.Fatalf("in-memory limiter must stay bounded, holds %d keys (limit %d) after %d distinct ones",
			counter.size(), limit, keys)
	}

	// Просроченное окно обязано переоткрыться: иначе память растёт по времени
	// сбоя, а не по числу запросов. Проверяем НАБЛЮДАЕМОЕ поведение, а не то,
	// что запись удалилась из карты.
	short := newMemoryCounter(memoryFallbackMax)
	if !short.allow("rl:/v1/share:ip:10.1.1.1", 1, 1) {
		t.Fatal("first request in the window must pass")
	}
	if short.allow("rl:/v1/share:ip:10.1.1.1", 1, 1) {
		t.Fatal("second request in the same window must be limited")
	}
	reset := false
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if short.allow("rl:/v1/share:ip:10.1.1.1", 1, 1) {
			reset = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !reset {
		t.Fatal("expired window must reset: the request budget is stuck")
	}
}

// TestInMemoryLimiterCountsPerWindow — локальный счётчик повторяет семантику
// windowLua: новое окно после истечения и предел Max внутри окна.
func TestInMemoryLimiterCountsPerWindow(t *testing.T) {
	counter := newMemoryCounter(16)
	for i := 0; i < 3; i++ {
		if !counter.allow("k", 60, 3) {
			t.Fatalf("request %d must pass", i)
		}
	}
	if counter.allow("k", 60, 3) {
		t.Fatal("4th request in the window must be limited")
	}
	if !counter.allow("other", 60, 1) {
		t.Fatal("another key must have its own window")
	}
}
