// Refund отзывает entitlement по payment_id (A11/F-25, refund-сторона находки).
//
// Пара к тестам в пакете auth: merge теперь не удаляет строку подписки, за
// которой стоит платёж, поэтому связка «payment_id → subscriptions» доходит до
// возврата. Этот тест фиксирует вторую половину цепочки: при наличии связи
// finalizeRefund обязан отозвать дни, а при её отсутствии — не тишинить.
package payments

import (
	"context"
	"testing"

	"taro/api/internal/me"
	"taro/api/internal/testutil"
)

func TestFinalizeRefundRevokesSubscriptionLinkedByPayment(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, me.New(pg, rd))

	uid := testutil.NewUser(t, ctx, pg)
	var planID string
	if err := pg.QueryRow(ctx, `SELECT id FROM (SELECT DISTINCT ON (code) id, code FROM plans WHERE code='month_299' ORDER BY code, valid_from DESC) x`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	var payID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider,
		                      provider_payment_id, amount_rub, stars, status, idempotency_key,
		                      duration_days_snapshot)
		SELECT $1, $2, 'month_299', 299, 'tg_stars', 'a11p:' || gen_random_uuid(), 299, 199,
		       'succeeded', $3, 30
		RETURNING id::text`, uid, planID, "a11p-"+t.Name()).Scan(&payID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM payment_webhook_events WHERE payment_id=$1`, payID)
		_, _ = pg.Exec(context.Background(), `DELETE FROM payment_refunds WHERE payment_id=$1`, payID)
		_, _ = pg.Exec(context.Background(), `DELETE FROM payments WHERE id=$1`, payID)
	})
	if _, err := pg.Exec(ctx, `
		INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, valid_until, payment_id, source_type)
		VALUES ($1,$2,'month_299',299, now() + interval '30 days', $3, 'payment')`, uid, planID, payID); err != nil {
		t.Fatal(err)
	}

	// Возврат подтверждён → платёж в refunding → finalize отзывает дни.
	if _, err := pg.Exec(ctx,
		`UPDATE payments SET status='refunding', refund_state='requested', refund_requested_at=now() WHERE id=$1`, payID); err != nil {
		t.Fatal(err)
	}
	// requested → confirmed (юзер подтвердил у Telegram) → finalize
	if _, err := pg.Exec(ctx,
		`UPDATE payments SET refund_state='confirmed', refund_attempted_at=now() WHERE id=$1`, payID); err != nil {
		t.Fatal(err)
	}
	if err := svc.finalizeRefund(ctx, payID); err != nil {
		t.Fatalf("finalizeRefund: %v", err)
	}

	var active int
	if err := pg.QueryRow(ctx,
		`SELECT count(*) FROM subscriptions
		  WHERE user_id=$1 AND payment_id=$2 AND status='active' AND valid_until > now() + interval '29 days'`,
		uid, payID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatalf("refund left the entitlement active: %d rows", active)
	}
	var status string
	if err := pg.QueryRow(ctx, `SELECT status FROM payments WHERE id=$1`, payID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "refunded" {
		t.Fatalf("payment status after finalize: %q", status)
	}
}

// Контр-случай: если связи с платежом нет (строка уже была потеряна — именно
// то, что делал старый merge), возврат не должен молча «отзывать» что-то
// чужое. Документирует, почему сохранение provenance в merge обязательно.
func TestFinalizeRefundWithoutLinkRevokesNothing(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, me.New(pg, rd))
	uid := testutil.NewUser(t, ctx, pg)
	var planID string
	if err := pg.QueryRow(ctx, `SELECT id FROM (SELECT DISTINCT ON (code) id, code FROM plans WHERE code='month_299' ORDER BY code, valid_from DESC) x`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	var payID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider,
		                      provider_payment_id, amount_rub, stars, status, idempotency_key)
		SELECT $1, $2, 'month_299', 299, 'tg_stars', 'a11q:' || gen_random_uuid(), 299, 199,
		       'succeeded', $3
		RETURNING id::text`, uid, planID, "a11q-"+t.Name()).Scan(&payID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM payment_webhook_events WHERE payment_id=$1`, payID)
		_, _ = pg.Exec(context.Background(), `DELETE FROM payment_refunds WHERE payment_id=$1`, payID)
		_, _ = pg.Exec(context.Background(), `DELETE FROM payments WHERE id=$1`, payID)
	})
	// Подписка есть, но без payment_id — как если бы merge её съел.
	if _, err := pg.Exec(ctx, `
		INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, valid_until, source_type)
		VALUES ($1,$2,'month_299',0, now() + interval '30 days', 'legacy')`, uid, planID); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx,
		`UPDATE payments SET status='refunding', refund_state='requested', refund_requested_at=now() WHERE id=$1`, payID); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx,
		`UPDATE payments SET refund_state='confirmed', refund_attempted_at=now() WHERE id=$1`, payID); err != nil {
		t.Fatal(err)
	}
	if err := svc.finalizeRefund(ctx, payID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := pg.QueryRow(ctx,
		`SELECT count(*) FROM subscriptions WHERE user_id=$1 AND status='active' AND valid_until > now() + interval '29 days'`,
		uid).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 1 {
		t.Fatalf("without a payment link nothing may be revoked, but %d rows changed", left)
	}
}
