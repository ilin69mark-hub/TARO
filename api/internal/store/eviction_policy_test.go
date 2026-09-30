// A16/F-05: контракт политики вытеснения Redis.
//
// Инвариант: вытеснение ключа НЕ ДОЛЖНО обнулять окно rate-limit и не должно
// разлогинивать. На общем инстансе с allkeys-lru это происходило: ballast-заливка
// до maxmemory удаляла `rl:*` (EXISTS → 0), и клиент получал новый бюджет.
//
// Контракт проверяется на двух уровнях:
//   - репозиторий: compose-файлы обязаны объявлять noeviction (работает везде);
//   - живой сервер: maxmemory-policy реально noeviction (пропускается, если
//     CONFIG запрещён управляемым Redis — тогда верхний уровень остаётся).
package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stripYAMLComments убирает комментарии, чтобы проверка смотрела на
// АКТУАЛЬНЫЕ аргументы redis-server, а не на текст объяснения рядом. Правило
// простое: строка, начинающаяся с `#`, выбрасывается целиком, иначе режется от
// первого ` #`. Кавычки не разбираем: в этих compose-файлах нет значений с
// пробелом перед `#`, а лишняя сложность тут только создала бы новый класс
// ложных срабатываний.
func stripYAMLComments(text string) string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if idx := strings.Index(line, " #"); idx >= 0 {
			line = line[:idx]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func TestComposeDeclaresNoEvictionPolicy(t *testing.T) {
	// A16/F-05: allkeys-lru на общем инстансе вытеснял rl:*/sess:* и молча
	// сбрасывал окно rate-limit. Возврат этой политики — регрессия безопасности.
	forbidden := []string{"allkeys-lru", "allkeys-lfu", "allkeys-random", "volatile-lru", "volatile-lfu", "volatile-random"}
	files := []string{
		filepath.Join("..", "..", "..", "docker-compose.yml"),
		filepath.Join("..", "..", "..", "deploy", "docker-compose.prod.yml"),
		filepath.Join("..", "..", "..", "deploy", "docker-compose.tls.yml"),
	}
	checked := 0
	for _, path := range files {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := stripYAMLComments(string(raw))
		if !strings.Contains(text, "redis-server") {
			continue
		}
		checked++
		for _, policy := range forbidden {
			if strings.Contains(text, policy) {
				t.Fatalf("%s declares maxmemory-policy %s: eviction can reset rate-limit windows (A16/F-05)", path, policy)
			}
		}
		if !strings.Contains(text, "--maxmemory-policy noeviction") {
			t.Fatalf("%s starts redis-server without an explicit noeviction policy (A16/F-05)", path)
		}
	}
	if checked == 0 {
		t.Fatal("no compose file with redis-server was checked: the policy contract lost its teeth")
	}
}

// composeServiceBlock вырезает блок сервиса по отступам YAML (2 пробела на имя
// сервиса). Парсить YAML ради пары проверок нечем — в go.mod нет yaml-пакета,
// а формат compose здесь стабильный. Имя сравнивается с двоеточием, поэтому
// `cache` не матчит `cache-ai`.
func composeServiceBlock(text, name string) string {
	marker := "\n  " + name + ":"
	start := strings.Index(text, marker)
	if start < 0 {
		return ""
	}
	lines := strings.Split(text[start+len(marker):], "\n")
	end := len(lines)
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if indent := len(lines[i]) - len(strings.TrimLeft(lines[i], " ")); indent <= 2 {
			end = i
			break
		}
	}
	return strings.Join(lines[:end], "\n")
}

// TestComposeSeparatesCacheRoles — контракт ролей A17/F-43 на уровне репозитория:
// bulk-кэш обязан жить в отдельном инстансе со своей политикой вытеснения и без
// опубликованных портов, а оба API — получать его адрес. Единственное, что
// ловит удаление сервиса cache-ai из compose.
func TestComposeSeparatesCacheRoles(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	block := composeServiceBlock(text, "cache-ai")
	if block == "" {
		t.Fatal("docker-compose.yml has no cache-ai service: the ai_cache role is declared but not deployed (A17/F-43)")
	}
	if !strings.Contains(stripYAMLComments(block), "--maxmemory-policy volatile-ttl") {
		t.Fatal("cache-ai must evict by TTL: eviction is allowed only for the cache role, never for critical (A16/F-05)")
	}
	if !strings.Contains(stripYAMLComments(block), "--maxmemory ") {
		t.Fatal("cache-ai must declare an explicit memory budget (A17/F-43)")
	}
	if strings.Contains(block, "ports:") {
		t.Fatal("cache-ai must not publish ports: it is reachable only from the internal network")
	}
	if !strings.Contains(block, "redisdata-ai:") {
		t.Fatal("cache-ai needs its own volume, otherwise it shares persistence with the critical role")
	}
	// Оба API получают адрес роли, иначе api-admin не сможет показать ai_cache.
	for _, service := range []string{"api-public", "api-admin"} {
		block := composeServiceBlock(text, service)
		if !strings.Contains(block, "REDIS_AI_ADDR: cache-ai:6379") {
			t.Fatalf("%s must receive REDIS_AI_ADDR: cache-ai:6379 (A17/F-43)", service)
		}
	}
}

func TestLiveRedisHasNoEvictionPolicy(t *testing.T) {
	if os.Getenv("REDIS_ADDR") == "" {
		t.Skip("no REDIS_ADDR")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// ConnectRedis напрямую: testutil импортирует store, поэтому из store его
	// использовать нельзя (цикл импортов).
	rd := ConnectRedis()
	defer rd.Close()
	if err := rd.Ping(ctx).Err(); err != nil {
		t.Skipf("redis is not reachable: %v", err)
	}
	policy, err := rd.ConfigGet(ctx, "maxmemory-policy").Result()
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "noperm") || strings.Contains(strings.ToLower(err.Error()), "unknown") {
			t.Skipf("CONFIG is not available on this Redis (%v): upper-level compose check still applies", err)
		}
		t.Fatal(err)
	}
	if policy["maxmemory-policy"] != "noeviction" {
		t.Fatalf("live maxmemory-policy=%q, want noeviction: eviction resets rate-limit windows (A16/F-05)",
			policy["maxmemory-policy"])
	}
	// noeviction означает, что вместо вытеснения приходит ошибка записи. Это
	// осознанный размен: failClosed-маршруты отдают 503, а не обнуляют лимит.
	value, err := rd.Set(ctx, "store:eviction-probe", "1", 30*time.Second).Result()
	if err != nil {
		t.Fatalf("probe write must succeed on a healthy instance: %v", err)
	}
	if value != "OK" {
		t.Fatalf("probe write returned %q", value)
	}
	if err := rd.Del(context.Background(), "store:eviction-probe").Err(); err != nil {
		t.Fatal(err)
	}
}
