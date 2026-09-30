// Merge не должен уничтожать provenance платежа (A11/F-25).
//
// Раньше дедупликация в Link удаляла все дубли одного плана у loser'а, кроме
// строки с б��льшим valid_until. Платежная строка могла оказаться удалённой —
// тогда finalizeRefund (UPDATE subscriptions … WHERE payment_id=$1) не отзывал
// entitlement: возврат проходил, а доступ оставался. Дни при этом не терялись
// (60 дней до и после merge), терялась именно связь с платежом.
package auth

import (
	"context"
	"fmt"
	"taro/api/internal/testutil"
	"testing"
	"time"
)

// TestMergeKeepsPaymentProvenanceForRefund — ядро находки: после merge строка с
// payment_id обязана выжить, и возврат по этому платежу обязан отозвать дни.
func TestMergeKeepsPaymentProvenanceForRefund(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-0123456789abcdef0123456789")
	ctx, pg, rd, svc := liveAuth(t)
	_ = rd

	tgID := time.Now().UnixNano() - 2000000
	var survivor, loser string
	if err := pg.QueryRow(ctx,
		`INSERT INTO users (tg_id, fingerprint) VALUES ($1,$2) RETURNING id`,
		tgID, "a11-fp").Scan(&survivor); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx,
		`INSERT INTO users (anon_uuid, fingerprint) VALUES (gen_random_uuid(),$1) RETURNING id`,
		"a11-fp").Scan(&loser); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pg.Exec(bg, `DELETE FROM trial_grants WHERE tg_id=$1`, tgID)
		_, _ = pg.Exec(bg, `DELETE FROM users WHERE id IN ($1,$2)`, survivor, loser)
	})

	// Платёж и подписка loser'а: две строки одного плана, у одной есть payment_id.
	var planID string
	if err := pg.QueryRow(ctx,
		`SELECT id FROM (SELECT DISTINCT ON (code) id, code FROM plans WHERE code='month_299'
		   ORDER BY code, valid_from DESC) x`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	var payID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider,
		                      provider_payment_id, amount_rub, stars, status,
		                      duration_days_snapshot, idempotency_key)
		SELECT $1, $2, 'month_299', 299, 'tg_stars', 'a11:' || gen_random_uuid(), 299, 199,
		       'succeeded', 30, $3
		RETURNING id::text`, loser, planID, "a11-"+t.Name()).Scan(&payID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM payment_webhook_events WHERE payment_id=$1`, payID)
		_, _ = pg.Exec(context.Background(), `DELETE FROM payments WHERE id=$1`, payID)
	})

	// Платёжная строка — с меньшим valid_until (её и пытается удалить dedup).
	if _, err := pg.Exec(ctx, `
		INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, valid_until, payment_id, source_type)
		VALUES ($1,$2,'month_299',299, now() + interval '30 days', $3, 'payment')`, loser, planID, payID); err != nil {
		t.Fatal(err)
	}
	// Бонусная строка того же плана — с бОльшим valid_until (это keeper).
	if _, err := pg.Exec(ctx, `
		INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, valid_until, source_type)
		VALUES ($1,$2,'month_299',0, now() + interval '60 days', 'legacy')`, loser, planID); err != nil {
		t.Fatal(err)
	}

	mergedID, merged, err := svc.Link(ctx, loser, tgID, "a11-fp", false)
	if err != nil {
		t.Fatal(err)
	}
	if !merged || mergedID != survivor {
		t.Fatalf("merge result: %q %v", mergedID, merged)
	}

	var keptPaymentLink int
	if err := pg.QueryRow(ctx,
		`SELECT count(*) FROM subscriptions WHERE user_id=$1 AND payment_id=$2`, survivor, payID).Scan(&keptPaymentLink); err != nil {
		t.Fatal(err)
	}
	if keptPaymentLink != 1 {
		t.Fatalf("merge destroyed the payment linkage: %d rows left for payment %s", keptPaymentLink, payID)
	}

	// Дни не потерялись: суммарный entitlement не меньше 60 дней.
	var days int
	if err := pg.QueryRow(ctx, `
		SELECT COALESCE(extract(epoch FROM (max(valid_until) - now()))/86400, 0)::int
		  FROM subscriptions WHERE user_id=$1 AND status='active'`, survivor).Scan(&days); err != nil {
		t.Fatal(err)
	}
	if days < 59 {
		t.Fatalf("merge lost paid days: %d left", days)
	}

}

// TestMergeDoesNotLoseDaysForTwoPaidRows — если у loser'а две оплаченные строки
// одного плана, обе обязаны выжить: entitlement считается как max(valid_until),
// лишняя строка не мешает, зато provenance обоих платежей сохраняется.
func TestMergeDoesNotLoseDaysForTwoPaidRows(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-0123456789abcdef0123456789")
	ctx, pg, rd, svc := liveAuth(t)
	_ = rd
	tgID := time.Now().UnixNano() - 3000000
	var survivor, loser string
	if err := pg.QueryRow(ctx, `INSERT INTO users (tg_id, fingerprint) VALUES ($1,$2) RETURNING id`, tgID, "a11b-fp").Scan(&survivor); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `INSERT INTO users (anon_uuid, fingerprint) VALUES (gen_random_uuid(),$1) RETURNING id`, "a11b-fp").Scan(&loser); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pg.Exec(bg, `DELETE FROM trial_grants WHERE tg_id=$1`, tgID)
		_, _ = pg.Exec(bg, `DELETE FROM users WHERE id IN ($1,$2)`, survivor, loser)
	})
	var planID string
	if err := pg.QueryRow(ctx, `SELECT id FROM (SELECT DISTINCT ON (code) id, code FROM plans WHERE code='month_299' ORDER BY code, valid_from DESC) x`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	// две оплаченные строки одного плана, у каждой свой платёж.
	// provider_payment_id уникален на ПРОГОН: фиксированное значение переживало
	// `go test -count=2` и второй прогон падал на payments_provider_payment_id_key
	// (F-18.4 — тесты должны иметь уникальные идентичности, а не надеяться на
	// чистую базу).
	nonce := testutil.UUID(t)
	for i := 1; i <= 2; i++ {
		var payID string
		if err := pg.QueryRow(ctx, `
			INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider,
			                      provider_payment_id, amount_rub, stars, status, idempotency_key)
			SELECT $1, $2, 'month_299', 299, 'tg_stars', $3, 299, 199, 'succeeded', $4
			RETURNING id::text`, loser, planID, fmt.Sprintf("a11b:%s:%d", nonce, i), fmt.Sprintf("a11b-%s-%d", nonce[:8], i)).Scan(&payID); err != nil {
			t.Fatal(err)
		}
		if _, err := pg.Exec(ctx, `
			INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, valid_until, payment_id, source_type)
			VALUES ($1,$2,'month_299',299, now() + interval '30 days', $3, 'payment')`, loser, planID, payID); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := svc.Link(ctx, loser, tgID, "a11b-fp", false); err != nil {
		t.Fatal(err)
	}
	var links, days int
	if err := pg.QueryRow(ctx,
		`SELECT count(*) FROM subscriptions WHERE user_id=$1 AND payment_id IS NOT NULL`, survivor).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `
		SELECT COALESCE(extract(epoch FROM (max(valid_until) - now()))/86400, 0)::int
		  FROM subscriptions WHERE user_id=$1 AND status='active'`, survivor).Scan(&days); err != nil {
		t.Fatal(err)
	}
	if links != 2 {
		t.Fatalf("both paid rows must survive the merge, got %d", links)
	}
	if days < 29 {
		t.Fatalf("paid days lost: %d", days)
	}
}
