// A16/F-05: граница памяти для bulk-ключей AI-кэша.
//
// Находка была не «Redis вытесняет», а «вытеснять нечего, если bulk не растёт
// без предела»: кэш ответов живёт 7 суток (CacheTTL) и на 200 МБ maxmemory
// делил инстанс с ключами лимитов (rl:*), сессий (sess:*, csrf:*) и
// entitlement-счётчиков (ent:*) при политике allkeys-lru. Замер дефекта: после
// ballast-заливки до maxmemory ключ `rl:/v1/spreads:ip:…` исчезал (EXISTS → 0),
// то есть окно rate-limit молча обнулялось и клиент получал новый бюджет.
//
// Поэтому здесь две вещи:
//  1. cacheKeyPrefix вынесен в константу — обрезка обязана видеть ровно то же
//     пространство ключей, что и запись, иначе она срезает чужое.
//  2. noteCacheEntry/trimCache держат число записей кэша в бюджете: при
//     превышении удаляются самые старые (наименьший остаток TTL). Обрезка
//     трогает ТОЛЬКО ai:cache:* — инвариант «вытеснение bulk не меняет лимиты
//     и не разлогинивает» проверяется тестом TestCacheTrimNeverTouchesCriticalKeys.
package ai

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// cacheKeyPrefix — пространство ключей кэша ответов AI.
	cacheKeyPrefix = "ai:cache:v3:"
	// defaultCacheMaxEntries — верхняя граница числа записей кэша. 5000 записей
	// по ~2 КБ занимают ~10 МБ из 200 МБ инстанса, то есть bulk перестаёт быть
	// источником вытеснения.
	defaultCacheMaxEntries = 5000
	minCacheMaxEntries     = 10
	maxCacheMaxEntries     = 1000000
	// cacheCountKey — счётчик записей. TTL намеренно длинный: короткий TTL
	// обнулял бы счётчик, и кэш успевал бы вырасти вдвое сверх бюджета.
	cacheCountKey  = "ai:cache:count"
	cacheCountTTL  = 24 * time.Hour
	cacheScanBatch = 256
	cacheScanRound = 64
	// cacheTrimNumerator/Denominator — после обрезки остаётся 9/10 бюджета,
	// чтобы подрезка случалась не на каждой записи.
	cacheTrimNumerator   = 9
	cacheTrimDenominator = 10
)

// cacheMaxEntries — бюджет записей кэша (AI_CACHE_MAX_ENTRIES).
func cacheMaxEntries() int64 {
	return int64(envInt("AI_CACHE_MAX_ENTRIES", defaultCacheMaxEntries, minCacheMaxEntries, maxCacheMaxEntries))
}

// noteCacheEntry учитывает новую запись кэша и при превышении бюджета подрезает
// самые старые. Ошибки Redis здесь НЕ ломают запись кэша: кэш — это ускоритель,
// а не источник истины.
func (g *Gateway) noteCacheEntry(ctx context.Context) {
	rd := g.cacheClient()
	if rd == nil {
		return
	}
	budget := cacheMaxEntries()
	count, err := rd.Incr(ctx, cacheCountKey).Result()
	if err != nil {
		return
	}
	_ = rd.Expire(ctx, cacheCountKey, cacheCountTTL).Err()
	if count <= budget {
		return
	}
	g.trimCache(ctx, budget, count)
}

// trimCache удаляет самые старые записи кэша, пока их не станет 9/10 бюджета.
// Сканирование ограничено по числу раундов: на больших инстансах лучше срезать
// меньше, чем блокировать Redis на полный обход.
func (g *Gateway) trimCache(ctx context.Context, budget, count int64) {
	rd := g.cacheClient()
	if rd == nil {
		return
	}
	target := budget * cacheTrimNumerator / cacheTrimDenominator
	if target < 1 {
		target = 1
	}
	type entry struct {
		key string
		ttl time.Duration
	}
	var entries []entry
	var cursor uint64
	for round := 0; round < cacheScanRound; round++ {
		keys, next, err := rd.Scan(ctx, cursor, cacheKeyPrefix+"*", cacheScanBatch).Result()
		if err != nil {
			return
		}
		if len(keys) > 0 {
			pipe := rd.Pipeline()
			ttls := make([]time.Duration, len(keys))
			for _, key := range keys {
				pipe.PTTL(ctx, key)
			}
			cmds, err := pipe.Exec(ctx)
			if err == nil {
				for i := range keys {
					if d, ok := cmds[i].(*redis.DurationCmd); ok {
						ttls[i], _ = d.Result()
					}
				}
			} else {
				ttls = ttls[:0]
			}
			for i, key := range keys {
				if i < len(ttls) {
					entries = append(entries, entry{key: key, ttl: ttls[i]})
				}
			}
		}
		cursor = next
		if cursor == 0 || int64(len(entries)) >= count {
			break
		}
	}
	if int64(len(entries)) <= target {
		return
	}
	// Наименьший остаток TTL = самая старая запись.
	sort.Slice(entries, func(i, j int) bool { return entries[i].ttl < entries[j].ttl })
	excess := int64(len(entries)) - target
	keys := make([]string, 0, excess)
	for i := int64(0); i < excess; i++ {
		keys = append(keys, entries[i].key)
	}
	removed, err := rd.Del(ctx, keys...).Result()
	if err != nil {
		return
	}
	remaining := count - removed
	if remaining < target {
		remaining = target
	}
	_ = rd.Set(ctx, cacheCountKey, remaining, cacheCountTTL).Err()
}

// cacheEntryCount — число записей по счётчику (для тестов и логов).
func (g *Gateway) cacheEntryCount(ctx context.Context) int64 {
	rd := g.cacheClient()
	if rd == nil {
		return 0
	}
	value, err := rd.Get(ctx, cacheCountKey).Result()
	if err != nil {
		return 0
	}
	count, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return count
}
