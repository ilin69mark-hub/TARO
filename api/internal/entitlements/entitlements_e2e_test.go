// E2E порядка EntitlementsService (см. D-покрытие, 02-functional/05).
package entitlements

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"taro/api/internal/testutil"
)

func TestE2ECheckOrder(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	s := New(pg, rd)

	u := testutil.NewUser(t, ctx, pg)

	// 1. daily free: allow → deny (лимит 1 из сида)
	v, err := s.Check(ctx, u, "daily")
	if err != nil || !v.Allow || v.Reason != "daily" {
		t.Fatalf("daily first: allow=%v reason=%s err=%v", v.Allow, v.Reason, err)
	}
	v, err = s.Check(ctx, u, "daily")
	if err != nil || v.Allow || v.Reason != "limit_exceeded" {
		t.Fatalf("daily second: allow=%v reason=%s err=%v", v.Allow, v.Reason, err)
	}

	// 2. love weekly: allow → deny
	v, err = s.Check(ctx, u, "love")
	if err != nil || !v.Allow || v.Reason != "love_weekly" {
		t.Fatalf("love first: %+v err=%v", v, err)
	}
	v, err = s.Check(ctx, u, "love")
	if err != nil || v.Allow {
		t.Fatalf("love second must deny: %+v err=%v", v, err)
	}

	// 3. premium без подписки/single → deny
	v, err = s.Check(ctx, u, "celtic")
	if err != nil || v.Allow {
		t.Fatalf("celtic free must deny: %+v err=%v", v, err)
	}

	// 4. активная подписка открывает всё
	if _, err := pg.Exec(ctx, `
		INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, valid_until)
		SELECT $1, id, 'month_299', 299, now() + interval '1 day' FROM plans WHERE code='month_299' LIMIT 1`,
		u); err != nil {
		t.Fatal(err)
	}
	v, err = s.Check(ctx, u, "celtic")
	if err != nil || !v.Allow || v.Reason != "subscription" {
		t.Fatalf("celtic premium: %+v err=%v", v, err)
	}

	// 5. неизвестный спред → ошибка (на чистом юзере: подписка проверяется первой!)
	u2 := testutil.NewUser(t, ctx, pg)
	if _, err := s.Check(ctx, u2, "nope"); err == nil {
		t.Fatal("unknown spread must error")
	}
}

func TestE2ESingle(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	s := New(pg, rd)
	u := testutil.NewUser(t, ctx, pg)

	// разовая покупка celtic: нужна реальная payment-строка
	var payID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, status)
		SELECT $1, id, 'single_99', 99, 'tg_stars', 'test:' || gen_random_uuid(), 99, 'succeeded'
		  FROM plans WHERE code='single_99' LIMIT 1 RETURNING id`, u).Scan(&payID); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx,
		`INSERT INTO single_entitlements (user_id, spread_code, payment_id) VALUES ($1,'celtic',$2)`,
		u, payID); err != nil {
		t.Fatal(err)
	}
	v, err := s.Check(ctx, u, "celtic")
	if err != nil || !v.Allow || v.Reason != "single" || v.SingleID == "" {
		t.Fatalf("single: %+v err=%v", v, err)
	}
	// другой premium-спред single celtic не открывает
	v, err = s.Check(ctx, u, "decision")
	if err != nil || v.Allow {
		t.Fatalf("decision must deny: %+v err=%v", v, err)
	}
}

func TestE2EPGConsume(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	s := New(pg, rd)
	u := testutil.NewUser(t, ctx, pg)

	// PG-fallback daily: allow, allow? limit 1 → deny второй
	ok, err := s.pgConsume(ctx, u, "daily", 1, mskDate(time.Now()))
	if err != nil || !ok {
		t.Fatalf("pg daily first: ok=%v err=%v", ok, err)
	}
	ok, err = s.pgConsume(ctx, u, "daily", 1, mskDate(time.Now()))
	if err != nil || ok {
		t.Fatalf("pg daily second must deny: ok=%v err=%v", ok, err)
	}
	// MSK-дата и понедельник sane
	if mondayMSK(time.Now()) == "" || mskDate(time.Now()) == "" {
		t.Fatal("empty dates")
	}
}

func TestE2ECheckFirstUseRaceUsesPG(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	s := New(pg, rd)
	u := testutil.NewUser(t, ctx, pg)
	var old json.RawMessage
	if err := pg.QueryRow(ctx, `SELECT value FROM app_config WHERE key='free.daily_limit'`).Scan(&old); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `UPDATE app_config SET value='"1"'::jsonb WHERE key='free.daily_limit'`); err != nil {
		t.Fatal(err)
	}
	redisKey := "ent:" + u + ":" + mskDate(time.Now())
	if err := rd.Set(ctx, redisKey, 99, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `UPDATE app_config SET value=$1 WHERE key='free.daily_limit'`, old)
		_ = rd.Del(context.Background(), redisKey).Err()
	})
	type outcome struct {
		verdict Verdict
		err     error
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			verdict, err := s.Check(ctx, u, "daily")
			results <- outcome{verdict: verdict, err: err}
		}()
	}
	wg.Wait()
	close(results)
	allow, deny := 0, 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("check: %v", result.err)
		}
		if result.verdict.Allow {
			allow++
		} else {
			deny++
		}
	}
	if allow != 1 || deny != 1 {
		t.Fatalf("allow=%d deny=%d", allow, deny)
	}
	var used int
	if err := pg.QueryRow(ctx, `SELECT free_used_today FROM entitlements WHERE user_id=$1`, u).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != 1 {
		t.Fatalf("pg used=%d", used)
	}
}
