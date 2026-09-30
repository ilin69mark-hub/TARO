// Конкурентность выдачи подписки (A07/F-07).
//
// До фикса два вебхука по разным платежам одного юзера блокировали разные
// строки payments, оба читали MAX(valid_until) и оба вставляли now()+30d —
// два оплаченных месяца давали 30 дней. Тест держит инвариант: N платежей
// одного юзера = N строк подписки и valid_until ≈ now() + 30·N дней.
package payments

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"taro/api/internal/me"
	"taro/api/internal/testutil"
)

func insertPaidForSubscription(t *testing.T, ctx context.Context, pg *pgxpool.Pool, uid, key string) storedPayment {
	t.Helper()
	var p storedPayment
	var duration *int
	if err := pg.QueryRow(ctx, `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider,
		                      provider_payment_id, amount_rub, stars, status, idempotency_key, purchase_fingerprint, duration_days_snapshot)
		SELECT $1, id, 'month_299', 299, 'tg_stars', 'a07:' || gen_random_uuid(), 299, 199, 'succeeded', $2, '', 30
		  FROM plans WHERE code='month_299' ORDER BY valid_from DESC LIMIT 1
		RETURNING id::text, user_id::text, plan_id::text, plan_code, price_rub_snapshot,
		          provider, provider_payment_id, amount_rub, stars, status, duration_days_snapshot`,
		uid, key).Scan(&p.ID, &p.UserID, &p.PlanID, &p.PlanCode, &p.Price,
		&p.Provider, &p.ProviderPaymentID, &p.Amount, &p.Stars, &p.Status, &duration); err != nil {
		t.Fatal(err)
	}
	p.Duration = duration
	t.Cleanup(func() { _, _ = pg.Exec(context.Background(), `DELETE FROM payments WHERE id=$1`, p.ID) })
	return p
}

func subsFor(t *testing.T, ctx context.Context, pg *pgxpool.Pool, uid string) (int, time.Time) {
	t.Helper()
	var n int
	var max time.Time
	if err := pg.QueryRow(ctx, `
		SELECT count(*), COALESCE(max(valid_until), 'epoch'::timestamptz)
		  FROM subscriptions WHERE user_id=$1 AND status='active'`, uid).Scan(&n, &max); err != nil {
		t.Fatal(err)
	}
	return n, max
}

// slowInsertTrigger расширяет окно между чтением MAX(valid_until) и вставкой:
// без него транзакции настолько короткие, что гонка воспроизводится
// нерегулярно (проверено: 8 прогонов без триггера проходят даже без фикса).
// С задержкой на INSERT второй воркер гарантированно читает MAX до коммита
// первого — то есть ровно то состояние, в котором теряются оплаченные дни.
func slowInsertTrigger(t *testing.T, ctx context.Context, pg *pgxpool.Pool) {
	t.Helper()
	const fn = "taro_test_slow_sub_insert"
	if _, err := pg.Exec(ctx, `
		CREATE OR REPLACE FUNCTION `+fn+`() RETURNS trigger AS $f$
		BEGIN
			PERFORM pg_sleep(0.4);
			RETURN NEW;
		END $f$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create trigger fn: %v", err)
	}
	if _, err := pg.Exec(ctx, `DROP TRIGGER IF EXISTS taro_test_slow_sub ON subscriptions`); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `
		CREATE TRIGGER taro_test_slow_sub BEFORE INSERT ON subscriptions
		FOR EACH ROW EXECUTE FUNCTION `+fn+`()`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pg.Exec(bg, `DROP TRIGGER IF EXISTS taro_test_slow_sub ON subscriptions`)
		_, _ = pg.Exec(bg, `DROP FUNCTION IF EXISTS `+fn+`()`)
	})
}

// TestGrantConcurrentWebhooksSameUser — два разных платежа одного юзера в двух
// параллельных транзакциях. В аудите гонка воспроизводилась в 11/12 прогонов
// реального хендлера и 40/40 на «голом» SQL.
func TestGrantConcurrentWebhooksSameUser(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, me.New(pg, rd))
	uid := testutil.NewUser(t, ctx, pg)
	slowInsertTrigger(t, ctx, pg)

	const workers = 2
	payments := make([]storedPayment, workers)
	for i := range payments {
		// idempotency_key уникален на платёж (idx_pay_user_idem) — иначе тест
		// падает на вставке, а не на проверяемой гонке.
		payments[i] = insertPaidForSubscription(t, ctx, pg, uid,
			fmt.Sprintf("a07-par-%s-%d", t.Name(), i))
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tx, err := pg.Begin(ctx)
			if err != nil {
				errs[i] = err
				return
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			<-start
			if err := svc.grantPaymentEntitlements(ctx, tx, payments[i]); err != nil {
				errs[i] = err
				return
			}
			errs[i] = tx.Commit(ctx)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
	}

	count, maxValid := subsFor(t, ctx, pg, uid)
	if count != workers {
		t.Fatalf("expected %d subscription rows, got %d", workers, count)
	}
	want := time.Now().Add(time.Duration(30*workers) * 24 * time.Hour)
	if delta := maxValid.Sub(want); delta < -time.Minute || delta > 5*time.Minute {
		t.Fatalf("valid_until lost days: got %s, want ~%s (delta %s)", maxValid.UTC(), want.UTC(), delta)
	}
}

// TestGrantSamePaymentTwiceIsNoop — повторная обработка одного платежа не должна
// создавать вторую строку (уникальный индекс idx_sub_payment_uniq, миграция 035).
func TestGrantSamePaymentTwiceIsNoop(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, me.New(pg, rd))
	uid := testutil.NewUser(t, ctx, pg)
	p := insertPaidForSubscription(t, ctx, pg, uid, "a07-dup-"+t.Name())

	for round := 0; round < 2; round++ {
		tx, err := pg.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.grantPaymentEntitlements(ctx, tx, p); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("round %d: %v", round, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("round %d commit: %v", round, err)
		}
	}

	count, maxValid := subsFor(t, ctx, pg, uid)
	if count != 1 {
		t.Fatalf("duplicate grant created %d rows, want 1", count)
	}
	if delta := maxValid.Sub(time.Now().Add(30 * 24 * time.Hour)); delta < -time.Minute || delta > 5*time.Minute {
		t.Fatalf("valid_until moved on duplicate grant: %s (delta %s)", maxValid.UTC(), delta)
	}
}

// Гонка проявится и без параллельных транзакций: два последовательных
// вызова для разных платежей обязаны дать 60 дней, а не 30. Это дешёвый
// инвариант, который ломается, если из выдачи выпадет advisory-блокировка или
// чтение перестанет опираться на предыдущие строки.
func TestGrantSequentialExtendsFromPrevious(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, me.New(pg, rd))
	uid := testutil.NewUser(t, ctx, pg)

	grant := func(key string) {
		p := insertPaidForSubscription(t, ctx, pg, uid, key)
		tx, err := pg.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.grantPaymentEntitlements(ctx, tx, p); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	grant("a07-seq-1-" + t.Name())
	grant("a07-seq-2-" + t.Name())

	count, maxValid := subsFor(t, ctx, pg, uid)
	if count != 2 {
		t.Fatalf("expected 2 rows, got %d", count)
	}
	want := time.Now().Add(60 * 24 * time.Hour)
	if delta := maxValid.Sub(want); delta < -time.Minute || delta > 5*time.Minute {
		t.Fatalf("second grant did not extend: %s vs %s (delta %s)", maxValid.UTC(), want.UTC(), delta)
	}
}

var _ = pgx.Tx(nil) // pgx используется в сигнатурах хелперов выше
