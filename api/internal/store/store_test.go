// Unit store: error-path + клиент (см. D-покрытие).
package store

import (
	"context"
	"testing"
)

func TestConnectPGErr(t *testing.T) {
	t.Setenv("DATABASE_URL", "://bad-url")
	if _, err := ConnectPG(context.Background()); err == nil {
		t.Fatal("want parse error")
	}
}

func TestConnectRedisClient(t *testing.T) {
	t.Setenv("REDIS_ADDR", "127.0.0.1:6391")
	c := ConnectRedis()
	if c == nil {
		t.Fatal("nil client")
	}
	_ = c.Close()
}
