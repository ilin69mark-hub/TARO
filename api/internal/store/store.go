// Package store — подключения к PG и Redis (см. docs/project-book/04-architecture/01).
package store

import (
	"context"
	"os"
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
	return redis.NewClient(&redis.Options{
		Addr:        os.Getenv("REDIS_ADDR"),
		Password:    os.Getenv("REDIS_PASSWORD"),
		DialTimeout: 2 * time.Second,
	})
}
