// Месячный кап реферальных бонусов (A10/F-24).
//
// Раньше счётчик читался, сравнивался и только потом инкрементировался
// (check-then-act): два конкурентных завершения реферала оба читали одно
// значение, оба проходили кап 30 дней и месяц набирал 33 дня. Теперь кап
// проверяется условным upsert'ом, то есть атомарно на уровне БД.
package referral

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"taro/api/internal/entitlements"
	"taro/api/internal/testutil"
)

// TestReferralCapConcurrentNeverExceeds30 — N параллельных завершений реферала
// для одного referrer: месячный счётчик обязан остаться ≤ 30 и равным сумме
// выданных бонусов.
func TestReferralCapConcurrentNeverExceeds30(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	_ = rd
	svc := New(pg, nil)

	if _, err := pg.Exec(ctx, `CREATE SEQUENCE IF NOT EXISTS tg_seq_a10`); err != nil {
		t.Fatal(err)
	}
	referrer := testutil.NewUser(t, ctx, pg)
	if _, err := pg.Exec(ctx, `UPDATE users SET tg_id = nextval('tg_seq_a10') WHERE id=$1`, referrer); err != nil {
		t.Fatal(err)
	}

	// Готовим рефералов: каждый — отдельный pending-рефералл на того же referrer.
	const workers = 8
	refereeIDs := make([]string, 0, workers)
	for i := 0; i < workers; i++ {
		referee := testutil.NewUser(t, ctx, pg)
		if _, err := pg.Exec(ctx, `UPDATE users SET tg_id = nextval('tg_seq_a10') WHERE id=$1`, referee); err != nil {
			t.Fatal(err)
		}
		if _, err := pg.Exec(ctx,
			`INSERT INTO referrals (referrer_id, referee_id, bonus_days, status, code)
			 VALUES ($1,$2,3,'pending',$3)`, referrer, referee, fmt.Sprintf("a10-%s-%d", t.Name(), i)); err != nil {
			t.Fatal(err)
		}
		refereeIDs = append(refereeIDs, referee)
	}

	// Задержка на вставке в entitlements — расширяет окно гонки.
	const fn = "taro_test_slow_ent"
	if _, err := pg.Exec(ctx, `
		CREATE OR REPLACE FUNCTION `+fn+`() RETURNS trigger AS $f$
		BEGIN
			PERFORM pg_sleep(0.3);
			RETURN NEW;
		END $f$ LANGUAGE plpgsql`); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `DROP TRIGGER IF EXISTS taro_test_slow_ent ON entitlements`); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `
		CREATE TRIGGER taro_test_slow_ent BEFORE INSERT ON entitlements
		FOR EACH ROW EXECUTE FUNCTION `+fn+`()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pg.Exec(bg, `DROP TRIGGER IF EXISTS taro_test_slow_ent ON entitlements`)
		_, _ = pg.Exec(bg, `DROP FUNCTION IF EXISTS `+fn+`()`)
	})

	var wg sync.WaitGroup
	for _, referee := range refereeIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			svc.CompleteOnFirstReading(ctx, id)
		}(referee)
	}
	wg.Wait()

	var monthUsed, lifetime int
	if err := pg.QueryRow(ctx,
		`SELECT COALESCE(referral_bonus_month,0), COALESCE(referral_bonus_lifetime,0)
		   FROM entitlements WHERE user_id=$1`, referrer).Scan(&monthUsed, &lifetime); err != nil {
		t.Fatal(err)
	}
	var completed int
	if err := pg.QueryRow(ctx,
		`SELECT count(*) FROM referrals WHERE referrer_id=$1 AND status='completed'`, referrer).Scan(&completed); err != nil {
		t.Fatal(err)
	}

	if monthUsed > 30 {
		t.Fatalf("monthly cap breached: %d > 30 (completed=%d, lifetime=%d)", monthUsed, completed, lifetime)
	}
	// счётчик месяца обязан совпадать с фактически выданным бонусом
	if monthUsed != completed*3 {
		t.Fatalf("counter %d != granted %d (completed=%d, lifetime=%d)", monthUsed, completed*3, completed, lifetime)
	}
	// lifetime обязан совпадать с суммой месячных начислений
	if lifetime != monthUsed {
		t.Fatalf("lifetime %d != month %d", lifetime, monthUsed)
	}
	t.Logf("ok: workers=%d month=%d completed=%d lifetime=%d", workers, monthUsed, completed, lifetime)
}

// Последовательный инвариант: бонусы накапливаются до кэпа, дальше рефералы
// отклоняются, а счётчик месяца не растёт.
func TestReferralCapRejectsAfterThirtyDays(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	_ = rd
	svc := New(pg, entitlements.New(pg, rd))
	if _, err := pg.Exec(ctx, `CREATE SEQUENCE IF NOT EXISTS tg_seq_a10b`); err != nil {
		t.Fatal(err)
	}
	referrer := testutil.NewUser(t, ctx, pg)
	if _, err := pg.Exec(ctx, `UPDATE users SET tg_id = nextval('tg_seq_a10b') WHERE id=$1`, referrer); err != nil {
		t.Fatal(err)
	}

	applyOne := func(i int) {
		referee := testutil.NewUser(t, ctx, pg)
		if _, err := pg.Exec(ctx, `UPDATE users SET tg_id = nextval('tg_seq_a10b') WHERE id=$1`, referee); err != nil {
			t.Fatal(err)
		}
		if _, err := pg.Exec(ctx,
			`INSERT INTO referrals (referrer_id, referee_id, bonus_days, status, code)
			 VALUES ($1,$2,3,'pending',$3)`, referrer, referee, fmt.Sprintf("a10b-%s-%d", t.Name(), i)); err != nil {
			t.Fatal(err)
		}
		svc.CompleteOnFirstReading(ctx, referee)
	}
	// 10 рефералов по 3 дня = 30 дней — ровно в кэп
	for i := 0; i < 10; i++ {
		applyOne(i)
	}
	var used, completed, rejected int
	if err := pg.QueryRow(ctx, `SELECT referral_bonus_month FROM entitlements WHERE user_id=$1`, referrer).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != 30 {
		t.Fatalf("expected the cap to be exactly full (30), got %d", used)
	}
	if err := pg.QueryRow(ctx,
		`SELECT count(*) FROM referrals WHERE referrer_id=$1 AND status='completed'`, referrer).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if completed != 10 {
		t.Fatalf("expected 10 completed before the cap, got %d", completed)
	}

	// следующий реферал должен быть отклонён, счётчик не растёт
	applyOne(100)
	if err := pg.QueryRow(ctx,
		`SELECT count(*) FROM referrals WHERE referrer_id=$1 AND status='rejected'`, referrer).Scan(&rejected); err != nil {
		t.Fatal(err)
	}
	if rejected == 0 {
		t.Fatal("referral over the cap must be rejected, none were")
	}
	if err := pg.QueryRow(ctx, `SELECT referral_bonus_month FROM entitlements WHERE user_id=$1`, referrer).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != 30 {
		t.Fatalf("counter grew past the cap: %d", used)
	}
	// бонусные дни не должны были начислиться рефереру после кэпа
	var days int
	if err := pg.QueryRow(ctx,
		`SELECT COALESCE(max(extract(epoch FROM (valid_until - now()))/86400), 0)::int
		   FROM subscriptions WHERE user_id=$1 AND status='active'`, referrer).Scan(&days); err != nil {
		t.Fatal(err)
	}
	if days > 31 {
		t.Fatalf("referrer got more days than the cap: %d", days)
	}
}
