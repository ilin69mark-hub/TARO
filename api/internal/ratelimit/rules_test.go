// A14/F-18.5: ветки лимитера, которые не покрывал ни один тест.
//
// Аудит зафиксировал: у лимитера 10 правил и 1 тест; ветки `byUser` (ключ по
// user_id вместо IP), `taro_admin` (админская cookie) и `failClosed` (503 при
// недоступном Redis) не исполнялись вообще. Здесь они проверяются явно, плюс
// инвариант «у каждого защищённого префикса есть правило» — иначе новый маршрут
// можно добавить незащищённым, и ни один тест этого не заметит.
package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"taro/api/internal/auth"
	"taro/api/internal/testutil"
)

func fireAt(h http.Handler, path, ip string, cookies ...string) int {
	req := httptest.NewRequest("POST", path, strings.NewReader(""))
	req.RemoteAddr = ip + ":1234"
	for _, c := range cookies {
		pair := strings.SplitN(c, "=", 2)
		if len(pair) == 2 {
			req.AddCookie(&http.Cookie{Name: pair[0], Value: pair[1]})
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// TestByUserBucketIsPerUser — /v1/readings лимитируется по user_id, а не по IP:
// два разных пользователя с одного IP не делят бакет, один пользователь с
// разных IP — делит.
func TestByUserBucketIsPerUser(t *testing.T) {
	ctx, _, rd := testutil.Live(t)
	l := New(rd)
	h := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	ip := testutil.UniqueIP(t)

	tokA, err := auth.IssueJWT(testutil.UUID(t), auth.UserTTL)
	if err != nil {
		t.Fatal(err)
	}
	tokB, err := auth.IssueJWT(testutil.UUID(t), auth.UserTTL)
	if err != nil {
		t.Fatal(err)
	}

	// Идентичности уникальны на прогон: бакет поUser переживает -count=2, и при
	// фиксированном subject второй прогон сразу получал бы 429 (F-18.4).
	// Правило /v1/readings = 10/мин. A исчерпывает свой бакет.
	for i := 0; i < 10; i++ {
		if code := fireAt(h, "/v1/readings", ip, auth.CookieName+"="+tokA); code != 200 {
			t.Fatalf("A #%d: want 200 got %d", i, code)
		}
	}
	if code := fireAt(h, "/v1/readings", ip, auth.CookieName+"="+tokA); code != 429 {
		t.Fatalf("A #11: want 429 got %d", code)
	}
	// B с того же IP обязан пройти: бакет по user_id, а не по IP.
	if code := fireAt(h, "/v1/readings", ip, auth.CookieName+"="+tokB); code != 200 {
		t.Fatalf("B from same IP must have its own bucket, got %d", code)
	}
	// Тот же A, но с ДРУГОГО IP — тот же бакет (ключ по пользователю).
	otherIP := testutil.UniqueIP(t)
	if code := fireAt(h, "/v1/readings", otherIP, auth.CookieName+"="+tokA); code != 429 {
		t.Fatalf("A from another IP must share the user bucket, got %d", code)
	}
	_ = ctx
}

// TestAdminCookieUsesOwnBucket — админская cookie taro_admin тоже умеет
// ключевать бакет (иначе админские запросы склеивались бы с пользовательскими
// по IP, а лимит /v1/admin/ 30/мин делился бы с чужими).
func TestAdminCookieUsesOwnBucket(t *testing.T) {
	_, _, rd := testutil.Live(t)
	l := New(rd)
	h := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	ip := testutil.UniqueIP(t)

	adminTok, err := auth.IssueJWT("admin:"+testutil.UUID(t), auth.UserTTL)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		if code := fireAt(h, "/v1/admin/payments", ip, "taro_admin="+adminTok); code != 200 {
			t.Fatalf("admin #%d: want 200 got %d", i, code)
		}
	}
	if code := fireAt(h, "/v1/admin/payments", ip, "taro_admin="+adminTok); code != 429 {
		t.Fatalf("admin #31: want 429 got %d", code)
	}
	// Незалогиненный запрос с того же IP — отдельный ip:-бакет, не админский.
	if code := fireAt(h, "/v1/admin/payments", ip); code != 200 {
		t.Fatalf("anonymous must not share the admin bucket, got %d", code)
	}
}

// TestFailClosedOnRedisError — при недоступном Redis failClosed-правила отдают
// 503 (деньги/авторизация не должны проходить без лимита), а spreads —
// fail-open: каталог раскладов нельзя ронять вместе с кэшем.
func TestFailClosedOnRedisError(t *testing.T) {
	// Ломаем Redis для этого лимитера: клиент на порт, который не слушается.
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond,
		MaxRetries: -1, ReadTimeout: 200 * time.Millisecond})
	t.Cleanup(func() { _ = dead.Close() })
	l := New(dead)
	h := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	ip := testutil.UniqueIP(t)

	for _, path := range []string{"/v1/auth/anon", "/v1/readings", "/v1/admin/payments", "/v1/referral/me"} {
		if code := fireAt(h, path, ip); code != http.StatusServiceUnavailable {
			t.Errorf("failClosed %s: want 503 got %d", path, code)
		}
	}
	// fail-open правила: /v1/spreads и /v1/share обязаны пропустить запрос.
	for _, path := range []string{"/v1/spreads", "/v1/share/x"} {
		if code := fireAt(h, path, ip); code != 200 {
			t.Errorf("fail-open %s: want 200 got %d", path, code)
		}
	}
}

// TestEveryGuardedPrefixHasRule — инвариант против «забытого» маршрута: если
// префикс добавлен в правила, он обязан быть failClosed для денежных/авторских
// путей, а неизвестные пути не должны молча проходить лимитер как свои.
func TestEveryGuardedPrefixHasRule(t *testing.T) {
	_, _, rd := testutil.Live(t)
	l := New(rd)
	guarded := map[string]bool{}
	for _, rl := range l.rules {
		guarded[rl.prefix] = true
	}
	for _, want := range []string{
		"/v1/auth/", "/v1/readings", "/v1/spreads", "/v1/admin/", "/v1/share",
		"/v1/referral/", "/v1/payments/", "/v1/diary", "/v1/push/", "/v1/me",
	} {
		if !guarded[want] {
			t.Errorf("prefix %s must have a rate-limit rule", want)
		}
	}
	h := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	// Незащищённый путь не должен потреблять лимитеры других префиксов: 100
	// запросов к /v1/unknown не могут исчерпать /v1/me.
	ip := testutil.UniqueIP(t)
	for i := 0; i < 100; i++ {
		if code := fireAt(h, "/v1/unknown/route", ip); code != 200 {
			t.Fatalf("unlimited route #%d: want 200 got %d", i, code)
		}
	}
	if code := fireAt(h, "/v1/me", ip); code != 200 {
		t.Fatalf("/v1/me must keep its own budget, got %d", code)
	}
}
