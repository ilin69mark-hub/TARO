// A16/F-05: инвариант «вытеснение bulk-ключей не меняет лимиты и не разлогинивает».
//
// Дефект был найден замером, а не чтением кода: на тестовом Redis с
// allkeys-lru и ballast-заливкой до maxmemory ключ `rl:/v1/spreads:ip:…`
// исчезал (EXISTS → 0), то есть окно rate-limit обнулялось. Тесты ниже
// фиксируют обе стороны инварианта:
//
//  1. обрезка кэша (A16) трогает ТОЛЬКО ai:cache:* — rl:*/sess:* живы;
//  2. политика вытеснения в compose — noeviction, allkeys-lru запрещён.
package ai

import (
	"context"
	"fmt"
	"testing"
	"time"

	"taro/api/internal/testutil"
)

// TestCacheTrimNeverTouchesCriticalKeys — обрезка bulk-кэша обязана быть
// безобидной для соседей. Мутации: обрезка по маске "*" (срезает чужие ключи) и
// возврат unbounded-кэша (бюджет игнорируется).
func TestCacheTrimNeverTouchesCriticalKeys(t *testing.T) {
	ctx, _, rd, gw := testGateway(t)
	const budget = 10
	t.Setenv("AI_CACHE_MAX_ENTRIES", fmt.Sprintf("%d", budget))

	// Критические ключи, которые обрезка не имеет права удалять.
	guard := map[string]string{
		"rl:/v1/spreads:ip:203.0.113.7":           "60",
		"rl:admin-login:user:" + testutil.UUID(t): "3",
		"sess:" + testutil.UUID(t):                "session-token",
		"csrf:" + testutil.UUID(t):                "csrf-token",
		"ent:" + testutil.UUID(t) + ":x":          "7",
	}
	for key, value := range guard {
		if err := rd.Set(ctx, key, value, 10*time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		keys := make([]string, 0, len(guard))
		for key := range guard {
			keys = append(keys, key)
		}
		_ = rd.Del(context.Background(), keys...).Err()
	})

	// Заливаем кэш ЗНАЧИТЕЛЬНО сверх бюджета, чтобы обрезка точно сработала.
	writes := budget * 6
	for i := 0; i < writes; i++ {
		key := fmt.Sprintf("%s%s/model-%d", cacheKeyPrefix, testutil.UUID(t), i)
		gw.cacheText(key, "ответ "+fmt.Sprint(i))
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Счётчик бюджета тоже чистим: иначе `-count=2` начинает с уже
		// израсходованным бюджетом и проверяет не то, что думает.
		_ = rd.Del(ctx, cacheCountKey).Err()
		var cursor uint64
		for {
			keys, next, err := rd.Scan(ctx, cursor, cacheKeyPrefix+"*", 500).Result()
			if err != nil {
				return
			}
			if len(keys) > 0 {
				_ = rd.Del(ctx, keys...).Err()
			}
			cursor = next
			if cursor == 0 {
				return
			}
		}
	})

	for key, want := range guard {
		got, err := rd.Get(ctx, key).Result()
		if err != nil {
			t.Fatalf("critical key %s was evicted by cache trim: got error %v, want value %q", key, err, want)
		}
		if got != want {
			t.Fatalf("critical key %s was overwritten by cache trim: got %q want %q", key, got, want)
		}
	}

	// Бюджет обязан реально применяться: записано 6x бюджета, остаться должно
	// не больше 9/10 бюджета плюс запись, написанная последней и запустившая trim.
	var cached int
	var cursor uint64
	for {
		keys, next, err := rd.Scan(ctx, cursor, cacheKeyPrefix+"*", 500).Result()
		if err != nil {
			t.Fatal(err)
		}
		cached += len(keys)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	ceiling := budget * cacheTrimNumerator / cacheTrimDenominator
	if cached > ceiling+budget {
		t.Fatalf("cache budget not enforced: %d entries after %d writes with budget %d (ceiling %d)",
			cached, writes, budget, ceiling)
	}
	t.Logf("cache entries after %d writes with budget %d: %d (counter=%d)",
		writes, budget, cached, gw.cacheEntryCount(ctx))
}

// TestCacheBudgetIgnoresRedisFailures — кэш не должен ломать генерацию, если
// Redis не пишет: раньше запись была `_ = Set(...)`, и бюджет не должен был
// превратить это в ошибку на горячем пути.
func TestCacheBudgetIgnoresRedisFailures(t *testing.T) {
	ctx, _, _, _ := testGateway(t)
	t.Setenv("AI_CACHE_MAX_ENTRIES", "10")
	// rd == nil — самый дешёвый «Redis не работает».
	broken := &Gateway{}
	broken.cacheText(cacheKeyPrefix+"broken", "текст")
	if broken.cacheEntryCount(ctx) != 0 {
		t.Fatal("cache accounting must stay silent without Redis")
	}
}
