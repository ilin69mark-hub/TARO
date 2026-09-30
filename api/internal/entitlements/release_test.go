// Освобождение entitlement при терминальном провале (A12/F-12).
//
// Слот списывается в AuthorizeReading ДО вызова провайдера. Если генерация
// упала (сталив, попытки исчерпаны, pending_fallback → failed), слот обязан
// вернуться: иначе пользователь теряет бесплатное чтение или оплаченный
// single за неоказанную услугу.
package entitlements

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"taro/api/internal/testutil"
)

// releaseFixture создаёт пользователя с дневным лимитом 1 и одним pending-чтением,
// которое уже авторизовано (слот списан).
func releaseFixture(t *testing.T, ctx context.Context, pg *pgxpool.Pool, uid, spread string) string {
	t.Helper()
	var id string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, seed, algo_version,
		                      status, interpretation, quota_state)
		VALUES ($1,$2,'', '[{"card_id":1,"reversed":false,"position":0}]',1,1,
		        'pending','','unchecked')
		RETURNING id::text`, uid, spread).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestReleaseDailySlotOnTerminalFailure(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	en := New(pg, rd)
	uid := testutil.NewUser(t, ctx, pg)

	// спред без подписки: daily-квота
	readingID := releaseFixture(t, ctx, pg, uid, "daily")

	verdict, err := en.AuthorizeReading(ctx, readingID, uid, "daily")
	if err != nil || !verdict.Allow {
		t.Fatalf("authorize: verdict=%+v err=%v", verdict, err)
	}
	var used int
	if err := pg.QueryRow(ctx,
		`SELECT free_used_today FROM entitlements WHERE user_id=$1`, uid).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != 1 {
		t.Fatalf("daily slot must be consumed by authorization, free_used_today=%d", used)
	}

	released, err := en.ReleaseReadingAuthorization(ctx, readingID)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if !released {
		t.Fatal("release must report a refunded slot")
	}
	if err := pg.QueryRow(ctx,
		`SELECT free_used_today FROM entitlements WHERE user_id=$1`, uid).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != 0 {
		t.Fatalf("daily slot not returned: free_used_today=%d", used)
	}

	// Повторная отмена ничего не делает (идемпотентность).
	again, err := en.ReleaseReadingAuthorization(ctx, readingID)
	if err != nil {
		t.Fatalf("second release: %v", err)
	}
	if again {
		t.Fatal("second release must be a no-op")
	}
	if err := pg.QueryRow(ctx,
		`SELECT free_used_today FROM entitlements WHERE user_id=$1`, uid).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != 0 {
		t.Fatalf("double release consumed a slot it did not own: free_used_today=%d", used)
	}
}

func TestReleaseDailySlotThenReusableForNewReading(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	en := New(pg, rd)
	uid := testutil.NewUser(t, ctx, pg)
	first := releaseFixture(t, ctx, pg, uid, "daily")
	if v, err := en.AuthorizeReading(ctx, first, uid, "daily"); err != nil || !v.Allow {
		t.Fatalf("first authorize: %+v %v", v, err)
	}
	// Второе чтение сверх лимита — отказ.
	second := releaseFixture(t, ctx, pg, uid, "daily")
	verdict, err := en.AuthorizeReading(ctx, second, uid, "daily")
	if err != nil {
		t.Fatalf("second authorize: %v", err)
	}
	if verdict.Allow || verdict.Reason != "limit_exceeded" {
		t.Fatalf("second reading must be denied with limit_exceeded, got %+v", verdict)
	}
	// Провал первого возвращает слот — второе чтение снова проходит.
	if _, err := en.ReleaseReadingAuthorization(ctx, first); err != nil {
		t.Fatalf("release: %v", err)
	}
	third := releaseFixture(t, ctx, pg, uid, "daily")
	if v, err := en.AuthorizeReading(ctx, third, uid, "daily"); err != nil || !v.Allow {
		t.Fatalf("released slot must be reusable, got %+v err=%v", v, err)
	}
}

func TestReleaseSinglePurchaseOnTerminalFailure(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	en := New(pg, rd)
	uid := testutil.NewUser(t, ctx, pg)

	var planID string
	if err := pg.QueryRow(ctx, `SELECT id FROM (SELECT DISTINCT ON (code) id, code FROM plans WHERE code='single_99' ORDER BY code, valid_from DESC) x`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	var payID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider,
		                      provider_payment_id, amount_rub, stars, status, idempotency_key)
		SELECT $1, $2, 'single_99', 99, 'tg_stars', 'a12:' || gen_random_uuid(), 99, 66, 'succeeded', $3
		RETURNING id::text`, uid, planID, "a12-"+t.Name()).Scan(&payID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM payment_webhook_events WHERE payment_id=$1`, payID)
		_, _ = pg.Exec(context.Background(), `DELETE FROM payments WHERE id=$1`, payID)
	})
	var singleID string
	if err := pg.QueryRow(ctx,
		`INSERT INTO single_entitlements (user_id, spread_code, payment_id) VALUES ($1,'any',$2) RETURNING id::text`,
		uid, payID).Scan(&singleID); err != nil {
		t.Fatal(err)
	}

	readingID := releaseFixture(t, ctx, pg, uid, "celtic") // премиум-спред, единственный путь к single
	verdict, err := en.AuthorizeReading(ctx, readingID, uid, "celtic")
	if err != nil || !verdict.Allow {
		t.Fatalf("authorize: %+v %v", verdict, err)
	}
	var consumed *string
	if err := pg.QueryRow(ctx,
		`SELECT consumed_reading_id::text FROM single_entitlements WHERE id=$1`, singleID).Scan(&consumed); err != nil {
		t.Fatal(err)
	}
	if consumed == nil {
		t.Fatal("single purchase must be consumed by authorization")
	}

	if _, err := en.ReleaseReadingAuthorization(ctx, readingID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := pg.QueryRow(ctx,
		`SELECT consumed_reading_id::text FROM single_entitlements WHERE id=$1`, singleID).Scan(&consumed); err != nil {
		t.Fatal(err)
	}
	if consumed != nil {
		t.Fatalf("single purchase still consumed by %s", *consumed)
	}

	// Ключевая проверка: покупка, однажды освобождённая после провала, обязана
	// снова работать. Раньше уникальный индекс по entitlement_id (без учёта
	// released_at) делал покупку «навсегда израсходованной» — вторая попытка
	// падала на вставке квитанции, и деньги пользователя уходили в никуда.
	second := releaseFixture(t, ctx, pg, uid, "celtic")
	v2, err := en.AuthorizeReading(ctx, second, uid, "celtic")
	if err != nil {
		t.Fatalf("re-authorize after release: %v", err)
	}
	if !v2.Allow || v2.Reason != "single" {
		t.Fatalf("released single purchase must be reusable for a new reading, got %+v", v2)
	}
	if err := pg.QueryRow(ctx,
		`SELECT consumed_reading_id::text FROM single_entitlements WHERE id=$1`, singleID).Scan(&consumed); err != nil {
		t.Fatal(err)
	}
	if consumed == nil || *consumed != second {
		t.Fatalf("second reading must consume the single purchase, got %v", consumed)
	}
}

func TestReleaseDoesNotTouchSubscriptionEntitlement(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	en := New(pg, rd)
	uid := testutil.NewUser(t, ctx, pg)

	// Активная подписка: слот не списывается, отменять нечего.
	var planID string
	if err := pg.QueryRow(ctx, `SELECT id FROM (SELECT DISTINCT ON (code) id, code FROM plans WHERE code='month_299' ORDER BY code, valid_from DESC) x`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `
		INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, valid_until, source_type)
		VALUES ($1,$2,'month_299',299, now() + interval '30 days','legacy')`, uid, planID); err != nil {
		t.Fatal(err)
	}
	readingID := releaseFixture(t, ctx, pg, uid, "daily")
	verdict, err := en.AuthorizeReading(ctx, readingID, uid, "daily")
	if err != nil || !verdict.Allow || verdict.Reason != "subscription" {
		t.Fatalf("authorize: %+v %v", verdict, err)
	}
	released, err := en.ReleaseReadingAuthorization(ctx, readingID)
	if err != nil {
		t.Fatal(err)
	}
	if released {
		t.Fatal("a subscription reading consumes nothing, so there is nothing to release")
	}
}
