// Unit-тесты чистых функций entitlements (см. D2).
// Lua/consume — только против живого Redis (см. TestE2EConsumeLua).
package entitlements

import (
	"context"
	"os"
	"testing"
	"time"

	"taro/api/internal/store"
)

func TestMidnightMSK(t *testing.T) {
	msk, _ := time.LoadLocation("Europe/Moscow")
	// 2026-09-23 23:30 MSK → полночь 2026-09-24 00:00 MSK
	in := time.Date(2026, 9, 23, 23, 30, 0, 0, msk)
	got := time.Unix(midnightMSK(in), 0).In(msk)
	if got.Day() != 24 || got.Hour() != 0 || got.Minute() != 0 {
		t.Fatalf("want 2026-09-24 00:00 MSK, got %v", got)
	}
}

func TestWeekKeyBoundary(t *testing.T) {
	msk, _ := time.LoadLocation("Europe/Moscow")
	// 2024-12-29 (вс) и 2024-12-30 (пн) — разные ISO-недели
	a := weekKey(time.Date(2024, 12, 29, 12, 0, 0, 0, msk))
	b := weekKey(time.Date(2024, 12, 30, 12, 0, 0, 0, msk))
	if a == b {
		t.Fatalf("year boundary weeks equal: %s", a)
	}
	if weekKey(time.Date(2024, 12, 30, 12, 0, 0, 0, msk)) != weekKey(time.Date(2024, 12, 31, 12, 0, 0, 0, msk)) {
		t.Fatal("same week differs")
	}
}

// TestE2EConsumeLua — атомарность лимита против живого Redis (см. T11/D2).
func TestE2EConsumeLua(t *testing.T) {
	if os.Getenv("REDIS_ADDR") == "" {
		t.Skip("no REDIS_ADDR")
	}
	ctx := context.Background()
	rd := store.ConnectRedis()
	defer rd.Close()
	s := New(nil, rd)
	key := "test:lua:u1"
	_ = rd.Del(ctx, key).Err()
	ok1, err := s.consume(ctx, key, 1, time.Now().Add(time.Minute).Unix())
	if err != nil || !ok1 {
		t.Fatalf("first consume: ok=%v err=%v", ok1, err)
	}
	ok2, err := s.consume(ctx, key, 1, time.Now().Add(time.Minute).Unix())
	if err != nil || ok2 {
		t.Fatalf("second consume must fail: ok=%v err=%v", ok2, err)
	}
	_ = rd.Del(ctx, key).Err()
}
