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

func TestE2EReadingAuthorizationReceiptPreventsDoubleCharge(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	s := New(pg, rd)
	cases := []struct {
		name   string
		spread string
		column string
	}{
		{name: "daily", spread: "daily", column: "free_used_today"},
		{name: "love", spread: "love", column: "love_used_week"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := testutil.NewUser(t, ctx, pg)
			cards := `[{"card_id":1,"reversed":false,"position":0}]`
			var readingID string
			if err := pg.QueryRow(ctx, `
				INSERT INTO readings (user_id, spread_code, question, cards, seed, status, quota_state, worker_lease_until)
				VALUES ($1,$2,'receipt',$3,1,'pending','unchecked',now()+interval '2 minutes')
				RETURNING id`, u, tc.spread, cards).Scan(&readingID); err != nil {
				t.Fatal(err)
			}
			verdict, err := s.AuthorizeReading(ctx, readingID, u, tc.spread)
			if err != nil || !verdict.Allow {
				t.Fatalf("first authorization: %+v err=%v", verdict, err)
			}
			if _, err := pg.Exec(ctx, `UPDATE readings SET status='failed' WHERE id=$1 AND quota_state='allowed'`, readingID); err != nil {
				t.Fatal(err)
			}
			if _, err := pg.Exec(ctx, `
				UPDATE readings
				   SET status='pending', quota_state='unchecked', worker_claim_token=NULL,
				       worker_lease_until=now()+interval '2 minutes'
				 WHERE id=$1`, readingID); err != nil {
				t.Fatal(err)
			}
			verdict, err = s.AuthorizeReading(ctx, readingID, u, tc.spread)
			if err != nil || !verdict.Allow {
				t.Fatalf("retry authorization: %+v err=%v", verdict, err)
			}
			var used, receipts int
			if err := pg.QueryRow(ctx, `SELECT `+tc.column+` FROM entitlements WHERE user_id=$1`, u).Scan(&used); err != nil {
				t.Fatal(err)
			}
			if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM reading_authorization_receipts WHERE reading_id=$1`, readingID).Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			if used != 1 || receipts != 1 {
				t.Fatalf("used=%d receipts=%d", used, receipts)
			}
		})
	}
}

func TestE2ERecoverPendingAuthorization(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	s := New(pg, rd)
	u := testutil.NewUser(t, ctx, pg)
	cards := `[{"card_id":1,"reversed":false,"position":0}]`
	var readingID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, seed, status, quota_state, worker_lease_until, updated_at)
		VALUES ($1,'daily','recover',$2,1,'pending','unchecked',now()-interval '1 minute',now()-interval '10 minutes')
		RETURNING id`, u, cards).Scan(&readingID); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverPendingAuthorizations(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var status, quota string
	var used, receipts int
	if err := pg.QueryRow(ctx, `SELECT status, quota_state FROM readings WHERE id=$1`, readingID).Scan(&status, &quota); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `SELECT free_used_today FROM entitlements WHERE user_id=$1`, u).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM reading_authorization_receipts WHERE reading_id=$1`, readingID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || quota != "allowed" || used != 1 || receipts != 1 {
		t.Fatalf("status=%s quota=%s used=%d receipts=%d", status, quota, used, receipts)
	}
	if err := s.RecoverPendingAuthorizations(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var usedAgain int
	if err := pg.QueryRow(ctx, `SELECT free_used_today FROM entitlements WHERE user_id=$1`, u).Scan(&usedAgain); err != nil {
		t.Fatal(err)
	}
	if usedAgain != 1 {
		t.Fatalf("recovery repeated charge: used=%d", usedAgain)
	}
}

func TestE2ELegacyAllowedReadingGetsReceipt(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	s := New(pg, rd)
	u := testutil.NewUser(t, ctx, pg)
	cards := `[{"card_id":1,"reversed":false,"position":0}]`
	var readingID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, seed, status, quota_state, worker_lease_until)
		VALUES ($1,'daily','legacy-allowed',$2,1,'pending','allowed',now()+interval '2 minutes')
		RETURNING id`, u, cards).Scan(&readingID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthorizeReading(ctx, readingID, u, "daily"); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `UPDATE readings SET status='failed' WHERE id=$1`, readingID); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `UPDATE readings SET status='pending', quota_state='unchecked' WHERE id=$1`, readingID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthorizeReading(ctx, readingID, u, "daily"); err != nil {
		t.Fatal(err)
	}
	var receipts, counters int
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM reading_authorization_receipts WHERE reading_id=$1`, readingID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM entitlements WHERE user_id=$1`, u).Scan(&counters); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 || counters != 0 {
		t.Fatalf("receipts=%d counters=%d", receipts, counters)
	}
}

func TestE2EReadingAuthorizationLockOrder(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	s := New(pg, rd)
	u := testutil.NewUser(t, ctx, pg)
	var paymentID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, status)
		SELECT $1, id, 'single_99', 99, 'tg_stars', 'lock-order:' || gen_random_uuid(), 99, 'succeeded'
		  FROM plans WHERE code='single_99' ORDER BY valid_from DESC LIMIT 1
		RETURNING id`, u).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	var singleID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO single_entitlements (user_id, spread_code, payment_id)
		VALUES ($1,'decision',$2) RETURNING id::text`, u, paymentID).Scan(&singleID); err != nil {
		t.Fatal(err)
	}
	cards := `[{"card_id":1,"reversed":false,"position":0}]`
	var readingID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, seed, status, quota_state, worker_lease_until)
		VALUES ($1,'decision','lock-order',$2,1,'pending','unchecked',now()+interval '2 minutes')
		RETURNING id`, u, cards).Scan(&readingID); err != nil {
		t.Fatal(err)
	}
	blocker, err := pg.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	var lockedID string
	if err := blocker.QueryRow(ctx, `SELECT id::text FROM readings WHERE id=$1 FOR UPDATE`, readingID).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}
	authDone := make(chan error, 1)
	go func() {
		_, err := s.AuthorizeReading(context.Background(), readingID, u, "decision")
		authDone <- err
	}()
	time.Sleep(150 * time.Millisecond)
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	var lockedSingle string
	probeErr := blocker.QueryRow(probeCtx, `SELECT id::text FROM single_entitlements WHERE id=$1 FOR UPDATE`, singleID).Scan(&lockedSingle)
	probeCancel()
	if probeErr != nil {
		_ = blocker.Rollback(context.Background())
		<-authDone
		t.Fatalf("single lock was acquired before reading: %v", probeErr)
	}
	if err := blocker.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-authDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("authorization did not finish")
	}
	var linked string
	if err := pg.QueryRow(ctx, `SELECT COALESCE(consumed_reading_id::text,'') FROM single_entitlements WHERE id=$1`, singleID).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != readingID {
		t.Fatalf("linked reading=%q want=%q", linked, readingID)
	}
}

func TestE2EReadingAuthorizationLocksUserBeforeReading(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	s := New(pg, rd)
	u := testutil.NewUser(t, ctx, pg)
	cards := `[{"card_id":1,"reversed":false,"position":0}]`
	var readingID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, seed, status, quota_state)
		VALUES ($1,'daily','lock-user-first',$2,1,'pending','unchecked')
		RETURNING id`, u, cards).Scan(&readingID); err != nil {
		t.Fatal(err)
	}
	blocker, err := pg.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var lockedUser string
	if err := blocker.QueryRow(ctx, `SELECT id::text FROM users WHERE id=$1 FOR UPDATE`, u).Scan(&lockedUser); err != nil {
		_ = blocker.Rollback(context.Background())
		t.Fatal(err)
	}
	authCtx, authCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer authCancel()
	authDone := make(chan error, 1)
	go func() {
		_, err := s.AuthorizeReading(authCtx, readingID, u, "daily")
		authDone <- err
	}()
	time.Sleep(200 * time.Millisecond)
	select {
	case err := <-authDone:
		_ = blocker.Rollback(context.Background())
		t.Fatalf("authorization completed while user lock was held: %v", err)
	default:
	}
	probe, err := pg.Begin(ctx)
	if err != nil {
		_ = blocker.Rollback(context.Background())
		t.Fatal(err)
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	var lockedReading string
	probeErr := probe.QueryRow(probeCtx, `SELECT id::text FROM readings WHERE id=$1 FOR UPDATE`, readingID).Scan(&lockedReading)
	probeCancel()
	_ = probe.Rollback(context.Background())
	if probeErr != nil {
		_ = blocker.Rollback(context.Background())
		<-authDone
		t.Fatalf("reading lock was acquired before user lock: %v", probeErr)
	}
	if err := blocker.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-authDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("authorization did not finish")
	}
}
