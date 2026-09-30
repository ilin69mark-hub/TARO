// A18/F-42: бюджеты клиента Redis заданы ЯВНО. В go-redis нули означают не
// «по умолчанию безопасно», а дефолты библиотеки: `MaxRetries: 0` = «3 повтора
// с экспоненциальной паузой», из-за чего при мёртвом Redis один запрос платил
// ~1.7 с (замерено: 1.768 с на запрос, 20 запросов = 34.7 с).
package store

import (
	"os"
	"testing"
	"time"
)

func TestRedisClientHasExplicitBudgets(t *testing.T) {
	t.Setenv("REDIS_ADDR", "127.0.0.1:6379")
	client := ConnectRedis()
	defer client.Close()
	options := client.Options()

	// ВНИМАНИЕ к числам: go-redis НОРМАЛИЗУЕТ MaxRetries в Options.init() —
	// «-1» (повторы выключить) превращается во внутренний 0, а «0»
	// (не задано пользователем) превращается в 3. Поэтому в Options() мы видим
	// 0 при выключенных повторах и 3 при дефолтных. Проверяем именно это.
	if options.MaxRetries != 0 {
		t.Fatalf("MaxRetries=%d: 3 означает «повторять по умолчанию», что при мёртвом Redis даёт ~1.7 с на запрос (A18/F-42)", options.MaxRetries)
	}
	if options.ReadTimeout <= 0 {
		t.Fatalf("ReadTimeout=%s must be explicit", options.ReadTimeout)
	}
	if options.WriteTimeout <= 0 {
		t.Fatalf("WriteTimeout=%s must be explicit", options.WriteTimeout)
	}
	if options.PoolTimeout <= 0 {
		t.Fatalf("PoolTimeout=%s must be explicit: иначе ожидание свободного соединения не ограничено", options.PoolTimeout)
	}
	if !options.ContextTimeoutEnabled {
		t.Fatal("ContextTimeoutEnabled=false: context.WithTimeout не обрежет команду — бюджет лимитера не сработает")
	}
	if options.DialTimeout <= 0 || options.DialTimeout > 5*time.Second {
		t.Fatalf("DialTimeout=%s out of range", options.DialTimeout)
	}
	if options.PoolSize < 4 {
		t.Fatalf("PoolSize=%d is too small for a hot path", options.PoolSize)
	}
}

func TestRedisAIUsesTheSameBudgets(t *testing.T) {
	t.Setenv("REDIS_AI_ADDR", "127.0.0.1:6379/1")
	client, shared, err := ConnectRedisAI()
	if err != nil || shared {
		t.Fatalf("role client: shared=%v err=%v", shared, err)
	}
	defer client.Close()
	options := client.Options()
	if options.MaxRetries != 0 || options.ReadTimeout <= 0 || !options.ContextTimeoutEnabled {
		t.Fatalf("role ai_cache must share the explicit budgets: %+v", options)
	}
	if options.DB != 1 {
		t.Fatalf("role client DB=%d, want 1", options.DB)
	}
}

func TestRedisBudgetsAreNotSlowerThanTheyLook(t *testing.T) {
	// Бюджет лимитера — 50 мс, поэтому клиент не имеет права быть медленнее
	// самого бюджета ни на одном этапе: иначе ContextTimeoutEnabled будет
	// единственной защитой, а она срабатывает ПОСЛЕ ожидания.
	t.Setenv("REDIS_ADDR", "127.0.0.1:6379")
	client := ConnectRedis()
	defer client.Close()
	options := client.Options()
	if options.DialTimeout > 2*time.Second || options.ReadTimeout > 2*time.Second {
		t.Fatalf("client budgets (%s dial / %s read) exceed the limiter budget and only add waiting",
			options.DialTimeout, options.ReadTimeout)
	}
	if os.Getenv("REDIS_ADDR") == "" {
		t.Fatal("sanity: REDIS_ADDR must be set by t.Setenv")
	}
}
