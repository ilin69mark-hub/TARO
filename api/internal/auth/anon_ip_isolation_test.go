// Изоляция тестов: HandleAnon обязан видеть IP клиента.
//
// Историческая дыра (вскрыта регрессом A17): HandleAnon читал ТОЛЬКО X-Real-IP
// и подставлял литерал "unknown". Все e2e-тесты аутентификации выставляли
// RemoteAddr, поэтому КАЖДАЯ анонимная регистрация во всей кодовой базе
// попадала в один глобальный бакет `rl:reg:unknown` с TTL час и лимитом
// 20/час. Пока Redis был свежим, тесты проходили; на втором прогоне подряд
// получался честный 429 «Слишком много регистраций» — то есть результат тестов
// зависел от того, сколько раз их гоняли за последний час.
package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"taro/api/internal/store"
	"taro/api/internal/testutil"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newAnonIPRequest — минимальный запрос с заданными X-Real-IP и RemoteAddr.
func newAnonIPRequest(t *testing.T, header, remoteAddr string) *http.Request {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/auth/anon", strings.NewReader("{}"))
	if header != "" {
		req.Header.Set("X-Real-IP", header)
	}
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	return req
}

// TestAnonRegistrationNeverUsesSharedUnknownBucket — детектор возврата дефекта.
// Ключ живёт в общем Redis с часовым TTL, поэтому проверка не зависит от
// порядка тестов: если хоть одна регистрация в любом пакете прошла без
// разбираемого IP, ключ появится и следующий прогон (в т.ч. `-count=2`, который
// теперь обязателен) упадёт здесь.
func TestAnonRegistrationNeverUsesSharedUnknownBucket(t *testing.T) {
	if os.Getenv("REDIS_ADDR") == "" {
		t.Skip("no REDIS_ADDR")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rd := store.ConnectRedis()
	defer rd.Close()
	if err := rd.Ping(ctx).Err(); err != nil {
		t.Skipf("redis is not reachable: %v", err)
	}
	count, err := rd.Get(ctx, "rl:reg:unknown").Int64()
	if err != nil {
		// redis.Nil — ключа нет, это и есть желаемое состояние.
		return
	}
	t.Fatalf("shared anon bucket rl:reg:unknown was used (%d registrations): some test registers without a parseable client IP", count)
}

// TestHandleAnonKeysBucketBySocketPeer — проводка, а не только функция: исходный
// дефект жил в HandleAnon (X-Real-IP без fallback), поэтому юнит-тест на
// clientIP его не ловил. Здесь проверяется наблюдаемый эффект: после регистрации
// без заголовка X-Real-IP бакет обязан быть `rl:reg:<ip сокета>`, а НЕ общий
// `rl:reg:unknown`.
func TestHandleAnonKeysBucketBySocketPeer(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-0123456789abcdef0123456789")
	ctx, pg, rd, svc := liveAuth(t)

	ip := testutil.UniqueIP(t)
	req := httptest.NewRequest("POST", "/v1/auth/anon",
		strings.NewReader(`{"uuid":"`+newUUIDForTest(t, ctx, pg)+`","fingerprint":"anon-ip-fp"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = ip + ":34567"
	rec := httptest.NewRecorder()
	svc.HandleAnon(rec, req)
	if rec.Code != 200 {
		t.Fatalf("anon registration: want 200 got %d: %s", rec.Code, rec.Body.String())
	}

	if n, err := rd.Get(ctx, "rl:reg:"+ip).Int64(); err != nil || n < 1 {
		t.Fatalf("bucket rl:reg:%s must exist (got %v, err %v)", ip, n, err)
	}
	if n, err := rd.Get(ctx, "rl:reg:unknown").Int64(); err == nil {
		t.Fatalf("registration without X-Real-IP must not touch the shared bucket, got %d", n)
	}
	_ = rd.Del(context.Background(), "rl:reg:"+ip).Err()

	// Уборка юзера. HandleAnon создаёт строку, и раньше она оставалась навсегда:
	// на тесты, которые берут планы/профили по выборке из users, такой мусор —
	// случайная величина. Идентичность достаём из ответа, а не вычисляем заново.
	var created struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.UserID == "" {
		t.Fatalf("user_id в ответе anon: %v (%s)", err, rec.Body.String())
	}
	testutil.PurgeUsers(t, context.Background(), pg, created.UserID)
}

// newUUIDForTest — уникальный uuid для анонимной регистрации: фиксированный
// uuid переживал бы прогон и менял бы ветку кода (A14/F-18.4).
func newUUIDForTest(t *testing.T, ctx context.Context, pg *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pg.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return id
}

// TestClientIPFallsBackToRemoteAddr — контракт самой функции: приоритет
// X-Real-IP (его ставит nginx), затем адрес сокета, и только потом "unknown".
func TestClientIPFallsBackToRemoteAddr(t *testing.T) {
	cases := []struct {
		name       string
		header     string
		remoteAddr string
		want       string
	}{
		{name: "real ip header wins", header: "203.0.113.5", remoteAddr: "10.0.0.1:1234", want: "203.0.113.5"},
		{name: "header is normalised", header: " 2001:db8::1 ", remoteAddr: "10.0.0.1:1234", want: "2001:db8::1"},
		{name: "fallback to socket peer", header: "", remoteAddr: "10.0.0.7:5555", want: "10.0.0.7"},
		{name: "bare address without port", header: "", remoteAddr: "10.0.0.8", want: "10.0.0.8"},
		{name: "ipv6 socket peer", header: "", remoteAddr: "[2001:db8::2]:443", want: "2001:db8::2"},
		{name: "garbage header falls back", header: "not-an-ip", remoteAddr: "10.0.0.9:1", want: "10.0.0.9"},
		{name: "unparseable socket peer", header: "", remoteAddr: "garbage", want: "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := newAnonIPRequest(t, tc.header, tc.remoteAddr)
			if got := clientIP(req); got != tc.want {
				t.Fatalf("clientIP()=%q, want %q", got, tc.want)
			}
		})
	}
}
