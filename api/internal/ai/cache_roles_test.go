// A17/F-43: раздельные роли кэша. Кэш ответов AI (7 суток, bulk) обязан жить в
// своём пространстве ключей, иначе он делит память и риск с окнами лимитов и
// сессиями — ровно то, из-за чего F-05 сбрасывал окно rate-limit (A16).
package ai

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"taro/api/internal/store"
	"taro/api/internal/testutil"
)

// splitGateway — гейтвей с РАЗДЕЛЬНЫМИ ролями: critical в БД 0, кэш в БД 1.
// Именно это и есть прод-конфигурация (REDIS_AI_ADDR=host/N или отдельный
// инстанс), только обе роли на одном тестовом сервере.
func splitGateway(t *testing.T) (context.Context, *redis.Client, *redis.Client, *Gateway) {
	t.Helper()
	ctx, _, _, gw := testGateway(t)
	critical := gw.rd
	cache := newRoleClient(t, 1)
	return ctx, critical, cache, NewWithCache(nil, critical, cache)
}

// roleTestAddr — адрес роли на том же тестовом сервере, но в отдельной БД.
// Формат «host:port/N» — тот же контракт, что и REDIS_AI_ADDR в проде, поэтому
// тест проверяет РЕАЛЬНЫЙ код выделения роли, а не подмену конструктора.
func roleTestAddr(db int) string {
	base := strings.TrimSpace(os.Getenv("REDIS_ADDR"))
	return fmt.Sprintf("%s/%d", base, db)
}

func newRoleClient(t *testing.T, db int) *redis.Client {
	t.Helper()
	t.Setenv("REDIS_AI_ADDR", roleTestAddr(db))
	client, shared, err := store.ConnectRedisAI()
	if err != nil || shared {
		t.Fatalf("role client db=%d must be detached (shared=%v, err=%v)", db, shared, err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestBulkCachePressureKeepsSessionKeys — DoD A17: заполнение bulk-кэша не
// вытесняет rl:*/sess:*. Здесь это проверяется на РАЗДЕЛЬНЫХ пространствах
// ключей, поэтому утверждение сильнее исходного: кэш физически не может
// задеть ключи лимитов.
//
// Мутации: NewWithCache игнорирует cacheRd (кэш снова на общем инстансе) и
// выключенный бюджет (кэш растёт без границы).
func TestBulkCachePressureKeepsSessionKeys(t *testing.T) {
	ctx, critical, cache, gw := splitGateway(t)

	guard := map[string]string{
		"rl:/v1/spreads:ip:203.0.113.9":         "60",
		"rl:/v1/readings:u:" + testutil.UUID(t): "4",
		"sess:" + testutil.UUID(t):              "session-token",
		"csrf:" + testutil.UUID(t):              "csrf-token",
		"ent:" + testutil.UUID(t) + ":y":        "3",
	}
	for key, value := range guard {
		if err := critical.Set(ctx, key, value, 10*time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		keys := make([]string, 0, len(guard))
		for key := range guard {
			keys = append(keys, key)
		}
		_ = critical.Del(context.Background(), keys...).Err()
	})

	// Заливаем кэш вчетверо сверх бюджета — давление памяти создаётся честно.
	const budget = 10
	t.Setenv("AI_CACHE_MAX_ENTRIES", fmt.Sprintf("%d", budget))
	// Baseline, а не «ноль»: если чужой прогон оставил записи кэша в критическом
	// пространстве, тест обязан падать по СВОЕМУ утверждению, а не по чужому
	// мусору (иначе причина регрессии читается неверно).
	leakedBefore := countRole(t, critical, cacheKeyPrefix+"*")
	if leakedBefore > 0 {
		t.Logf("WARN: критическое пространство уже содержит %d записей кэша (остатки чужого прогона)", leakedBefore)
	}
	for i := 0; i < budget*4; i++ {
		gw.cacheText(fmt.Sprintf("%s%s/model-%d", cacheKeyPrefix, testutil.UUID(t), i), "ответ "+fmt.Sprint(i))
	}
	t.Cleanup(func() {
		purgeRole(t, cache, cacheKeyPrefix+"*")
		purgeRole(t, cache, cacheCountKey)
	})

	cached := countRole(t, cache, cacheKeyPrefix+"*")
	if cached == 0 {
		t.Fatal("cache role must hold entries: the cache was not written at all, the test would be vacuous")
	}
	// Ключи лимитов и сессий живы и не изменились.
	for key, want := range guard {
		got, err := critical.Get(ctx, key).Result()
		if err != nil {
			t.Fatalf("critical key %s lost under cache pressure: %v", key, err)
		}
		if got != want {
			t.Fatalf("critical key %s changed under cache pressure: got %q want %q", key, got, want)
		}
	}
	// ГЛАВНОЕ утверждение: в пространстве ключей лимитов не появилось ни одной
	// записи кэша. Если разделения нет — этот тест падает, даже когда память не
	// кончилась.
	if leaked := countRole(t, critical, cacheKeyPrefix+"*"); leaked != leakedBefore {
		t.Fatalf("cache entries must not live in the critical keyspace: %d new (was %d)", leaked-leakedBefore, leakedBefore)
	}
	ceiling := budget * cacheTrimNumerator / cacheTrimDenominator
	if cached > ceiling+budget {
		t.Fatalf("cache budget not enforced: %d entries with budget %d (ceiling %d)", cached, budget, ceiling)
	}
	t.Logf("cache role: %d entries (budget %d); critical role: %d guard keys intact", cached, budget, len(guard))
}

// TestCacheDisabledWithoutRole — без отдельной роли кэш выключен, а НЕ
// переехал молча на инстанс лимитов. Провайдер будет зван на каждый промпт,
// но это видимая цена, а не тихая порча разделения.
func TestCacheDisabledWithoutRole(t *testing.T) {
	ctx, _, rd, _ := testGateway(t)
	gw := NewWithCache(nil, rd, nil)
	key := cacheKeyPrefix + "no-role"
	gw.cacheText(key, "ответ")
	if _, err := rd.Get(ctx, key).Result(); err == nil {
		t.Fatal("cache must stay disabled, not fall back to the critical client")
	}
	if gw.cacheClient() != nil {
		t.Fatal("cache client must be nil when the role is not configured")
	}
}

// TestCacheStatsReportsRolesPerMemory — «метрика cache_bytes{role}» в единственной
// доступной форме: роли считаются отдельно и отдаются админкой.
func TestCacheStatsReportsRolesPerMemory(t *testing.T) {
	ctx, critical, cache, _ := splitGateway(t)

	rlKey := "rl:/v1/spreads:ip:198.51.100.77"
	sessKey := "sess:" + testutil.UUID(t)
	cacheKey := cacheKeyPrefix + "stats-probe"
	for _, kv := range []struct {
		rd     *redis.Client
		key    string
		value  string
		expiry time.Duration
	}{
		{critical, rlKey, "12", 10 * time.Minute},
		{critical, sessKey, "token", 10 * time.Minute},
		{cache, cacheKey, "cached-answer", time.Minute},
	} {
		if err := kv.rd.Set(ctx, kv.key, kv.value, kv.expiry).Err(); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = critical.Del(context.Background(), rlKey, sessKey).Err()
		_ = cache.Del(context.Background(), cacheKey).Err()
	})

	report := CacheStats(ctx, critical, cache)
	if !report.CacheDetached {
		t.Fatal("report must know the cache role is detached")
	}
	if report.CacheEntries != cacheMaxEntries() {
		t.Fatalf("report cache budget=%d, want %d", report.CacheEntries, cacheMaxEntries())
	}
	if report.CacheTTLSecs != int64(CacheTTL/time.Second) {
		t.Fatalf("report ttl=%d, want %d", report.CacheTTLSecs, int64(CacheTTL/time.Second))
	}
	byRole := map[string]RoleStats{}
	for _, role := range report.Roles {
		if role.Err != "" {
			t.Fatalf("role %s failed: %s", role.Role, role.Err)
		}
		byRole[role.Role] = role
	}
	criticalRole, ok := byRole["critical"]
	if !ok || criticalRole.Entries < 2 {
		t.Fatalf("critical role must count rl:*/sess:* keys, got %+v", criticalRole)
	}
	cacheRole, ok := byRole["ai_cache"]
	if !ok || cacheRole.Entries < 1 || cacheRole.Bytes <= 0 {
		t.Fatalf("ai_cache role must report entries and bytes, got %+v", cacheRole)
	}
	if !cacheRole.Approx || !criticalRole.Approx {
		t.Fatal("bytes are sampled and must be reported as approximate")
	}
	t.Logf("cache_bytes{role}: ai_cache=%d bytes / %d entries; critical=%d bytes / %d entries",
		cacheRole.Bytes, cacheRole.Entries, criticalRole.Bytes, criticalRole.Entries)
}

// TestCacheStatsRouteIsGuarded — статический guard: эндпоинт отчёта по кэшу
// показывает картину использования инстансов и обязан быть за RequireAdmin.
func TestCacheStatsRouteIsGuarded(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "cmd", "admin", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	const route = `Get("/v1/admin/cache"`
	if !strings.Contains(text, "ad.RequireAdmin)."+route) {
		t.Fatalf("cmd/admin/main.go must mount %s behind RequireAdmin", route)
	}
	if strings.Contains(text, "r.Get"+route) {
		t.Fatalf("cmd/admin/main.go must not expose %s without RequireAdmin", route)
	}
}

func countRole(t *testing.T, rd *redis.Client, pattern string) int {
	t.Helper()
	var total int
	var cursor uint64
	for {
		keys, next, err := rd.Scan(context.Background(), cursor, pattern, 500).Result()
		if err != nil {
			t.Fatal(err)
		}
		total += len(keys)
		cursor = next
		if cursor == 0 {
			return total
		}
	}
}

func purgeRole(t *testing.T, rd *redis.Client, pattern string) {
	t.Helper()
	var cursor uint64
	for {
		keys, next, err := rd.Scan(context.Background(), cursor, pattern, 500).Result()
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) > 0 {
			_ = rd.Del(context.Background(), keys...).Err()
		}
		cursor = next
		if cursor == 0 {
			return
		}
	}
}
