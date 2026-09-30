// Package store — подключения к PG и Redis (см. docs/project-book/04-architecture/01).
package store

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// ConnectPG открывает пул pgx. DATABASE_URL обязателен.
func ConnectPG(ctx context.Context) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 10
	return pgxpool.NewWithConfig(ctx, cfg)
}

// ConnectRedis открывает клиент Redis. REDIS_ADDR обязателен (например cache:6379).
// REDIS_PASSWORD опционален: если задан (и в compose у cache requirepass) — auth включен.
func ConnectRedis() *redis.Client {
	return ConnectRedisDB(0)
}

// redisTimeout — явные таймауты клиента Redis (A18/F-42). Раньше задавался
// только DialTimeout, а остальное бралось из нулей go-redis: `MaxRetries: 0`
// означает НЕ «без повторов», а «3 повтора по умолчанию» с экспоненциальной
// паузой. Итог: при мёртвом Redis один запрос платил ~1.7 с ожидания.
//
// MaxRetries: -1 отключает повторы полностью. Повторять бессмысленно: все
// команды приложения уже умеют деградировать (лимитер — 503 или счётчик в
// памяти, кэш — просто промах, сессия — отказ входа), а ожидание в 1.7 с
// превращается в исчерпание пула соединений под нагрузкой.
const (
	redisDialTimeout   = 2 * time.Second
	redisReadTimeout   = 2 * time.Second
	redisWriteTimeout  = 2 * time.Second
	redisPoolTimeout   = 2 * time.Second
	redisMaxRetries    = -1
	redisPoolSize      = 10
	redisMinIdleConns  = 2
	redisContextTimout = true
)

// redisOptions — единая конфигурация клиента для обеих ролей (A17/F-43).
func redisOptions(addr, password string, db int) *redis.Options {
	return &redis.Options{
		Addr:                  addr,
		Password:              password,
		DB:                    db,
		DialTimeout:           redisDialTimeout,
		ReadTimeout:           redisReadTimeout,
		WriteTimeout:          redisWriteTimeout,
		PoolTimeout:           redisPoolTimeout,
		MaxRetries:            redisMaxRetries,
		PoolSize:              redisPoolSize,
		MinIdleConns:          redisMinIdleConns,
		ContextTimeoutEnabled: redisContextTimout,
	}
}

// ConnectRedisDB — подключение к конкретной Redis-БД. Тесты используют отдельную
// БД на пакет, чтобы не мешать друг другу (A14/F-18.3); в проде это всегда 0.
func ConnectRedisDB(db int) *redis.Client {
	return redis.NewClient(redisOptions(os.Getenv("REDIS_ADDR"), os.Getenv("REDIS_PASSWORD"), db))
}

// ConnectRedisAI — клиент для bulk-кэша ответов AI (A17/F-43). Кэш на 7 суток
// делил инстанс с ключами лимитов, сессий и entitlement-счётчиков, поэтому у
// него теперь своя роль и свой бюджет памяти.
//
// REDIS_AI_ADDR понимает оба варианта:
//   - «cache-ai:6379» — отдельный инстанс (прод-контур);
//   - «host:6379/1»   — отдельная БД того же инстанса (тесты и dev).
//
// Второй вариант нужен не для экономии, а чтобы РАЗДЕЛЕНИЕ КЛЮЧЕЙ было
// проверяемо тестом, а не только задумано в compose.
//
// shared=true означает, что REDIS_AI_ADDR не задан и кэш вынужденно живёт на
// основном инстансе. Это деградация с потерей бюджета, а не тихая ошибка:
// вызывающий обязан предупредить на старте, потому что при noeviction (A16)
// общий инстанс ещё и упрётся в память.
func ConnectRedisAI() (client *redis.Client, shared bool, err error) {
	raw := strings.TrimSpace(os.Getenv("REDIS_AI_ADDR"))
	if raw == "" {
		return ConnectRedis(), true, nil
	}
	addr, db := raw, 0
	if idx := strings.LastIndex(raw, "/"); idx >= 0 {
		addr = raw[:idx]
		parsed, convErr := strconv.Atoi(raw[idx+1:])
		if convErr != nil || parsed < 0 || parsed > 15 {
			return nil, false, fmt.Errorf("REDIS_AI_ADDR: bad database index in %q", raw)
		}
		db = parsed
	}
	if addr == "" {
		return nil, false, fmt.Errorf("REDIS_AI_ADDR: empty host in %q", raw)
	}
	password := os.Getenv("REDIS_AI_PASSWORD")
	if password == "" {
		password = os.Getenv("REDIS_PASSWORD")
	}
	return redis.NewClient(redisOptions(addr, password, db)), false, nil
}
