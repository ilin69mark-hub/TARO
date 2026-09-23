// Общие хелперы живых тестов: PG+Redis из env (см. D-покрытие 70%).
// Локально: DATABASE_URL=...:5443 REDIS_ADDR=...:6391 (см. T15: хост-порты через env).
// В CI сервисы на 5432/6379 + migrate.sh до тестов (см. ci.yml).
package testutil

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"taro/api/internal/store"
)

// Live возвращает пул PG и Redis или Skip без env.
func Live(t *testing.T) (context.Context, *pgxpool.Pool, *redis.Client) {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("no DATABASE_URL")
	}
	ctx := context.Background()
	pg, err := store.ConnectPG(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pg.Close() })
	rd := store.ConnectRedis()
	t.Cleanup(func() { rd.Close() })
	var one int
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM spreads`).Scan(&one); err != nil {
		t.Fatalf("db not migrated (run deploy/migrate.sh up): %v", err)
	}
	return ctx, pg, rd
}

// NewUser создает anon-юзера, возвращает id. Чистит за собой.
func NewUser(t *testing.T, ctx context.Context, pg *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pg.QueryRow(ctx,
		`INSERT INTO users (anon_uuid) VALUES (gen_random_uuid()) RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id) })
	return id
}
