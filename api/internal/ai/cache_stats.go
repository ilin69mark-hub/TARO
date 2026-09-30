// A17/F-43: наблюдаемость кэша по ролям. Метрик-стека в проекте нет
// (prometheus не подключён), поэтому «метрика cache_bytes{role}» реализована
// тем, что в проекте реально можно: разбором ролей при обращении к admin API.
//
// Роли:
//   - ai_cache   — bulk ответов AI (BudgetRecords записей, TTL 7 суток);
//   - critical   — rl:*, sess:*, csrf:*, ent:* (окна лимитов и сессии).
//
// Bytes считаются по выборке (MEMORY USAGE с SAMPLES 1) и помечены approx:
// полный обход инстанса на каждый запрос админки стоил бы дороже, чем даёт
// точность. Entries — точное число ключей роли.
package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"

	"taro/api/internal/apierr"
)

// RoleStats — одна роль кэша.
type RoleStats struct {
	Role     string   `json:"role"`
	Entries  int64    `json:"entries"`
	Bytes    int64    `json:"bytes"`
	Approx   bool     `json:"approx"`
	Patterns []string `json:"patterns"`
	Err      string   `json:"error,omitempty"`
}

// CacheBudgetReport — что показывает отчёт.
type CacheBudgetReport struct {
	At            time.Time   `json:"at"`
	CacheEntries  int64       `json:"cache_entries_budget"`
	CacheTTLSecs  int64       `json:"cache_ttl_seconds"`
	Roles         []RoleStats `json:"roles"`
	CacheDetached bool        `json:"cache_detached"`
}

type roleSpec struct {
	name     string
	patterns []string
}

// criticalPatterns — ключи, которые нельзя терять ни при каких бюджетах.
var criticalPatterns = []string{"rl:*", "sess:*", "csrf:*", "ent:*"}

// CacheStats — снимок ролей. cacheRd == nil → роль ai_cache пропускается
// (кэш выключен), critical считается всегда, если передан клиент.
func CacheStats(ctx context.Context, criticalRd, cacheRd *redis.Client) CacheBudgetReport {
	report := CacheBudgetReport{
		At:            time.Now().UTC(),
		CacheEntries:  cacheMaxEntries(),
		CacheTTLSecs:  int64(CacheTTL / time.Second),
		CacheDetached: cacheRd != nil && cacheRd != criticalRd,
	}
	if cacheRd != nil {
		report.Roles = append(report.Roles, scanRole(ctx, cacheRd, roleSpec{name: "ai_cache", patterns: []string{cacheKeyPrefix + "*"}}))
	}
	if criticalRd != nil {
		report.Roles = append(report.Roles, scanRole(ctx, criticalRd, roleSpec{name: "critical", patterns: criticalPatterns}))
	}
	sort.Slice(report.Roles, func(i, j int) bool { return report.Roles[i].Role < report.Roles[j].Role })
	return report
}

func scanRole(ctx context.Context, rd *redis.Client, role roleSpec) RoleStats {
	stats := RoleStats{Role: role.name, Patterns: role.patterns, Approx: true}
	for _, pattern := range role.patterns {
		// SCAN принимает одну маску MATCH, поэтому каждая маска роли обходится
		// своим проходом курсора (одна выборка в 500 ключей недооценила бы
		// Entries на больших инстансах — это счётчик, а не метрика).
		var cursor uint64
		for {
			keys, next, err := rd.Scan(ctx, cursor, pattern, roleScanBatch).Result()
			if err != nil {
				stats.Err = err.Error()
				return stats
			}
			stats.Entries += int64(len(keys))
			stats.Bytes += sampleBytes(ctx, rd, keys)
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	return stats
}

const roleScanBatch = 500

// sampleBytes суммирует MEMORY USAGE по выборке ключей. Ошибка не ломает
// отчёт: Bytes остаётся частичным, Approx ужеtrue.
func sampleBytes(ctx context.Context, rd *redis.Client, keys []string) int64 {
	if len(keys) == 0 {
		return 0
	}
	pipe := rd.Pipeline()
	cmds := make([]*redis.IntCmd, len(keys))
	for i, key := range keys {
		cmds[i] = pipe.MemoryUsage(ctx, key, 1)
	}
	if _, err := pipe.Exec(ctx); err != nil && len(keys) == 0 {
		return 0
	}
	var total int64
	for _, cmd := range cmds {
		if usage, err := cmd.Result(); err == nil {
			total += usage
		}
	}
	return total
}

// HandleCacheStats — admin-эндпоинт GET /v1/admin/cache. ВНУТРИ RequireAdmin
// (проверяется статическим guard-тестом TestCacheStatsRouteIsGuarded, т.к.
// package main не импортируется).
func HandleCacheStats(criticalRd, cacheRd *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		probeCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		report := CacheStats(probeCtx, criticalRd, cacheRd)
		body, err := json.Marshal(report)
		if err != nil {
			apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось собрать статистику кэша")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	}
}
