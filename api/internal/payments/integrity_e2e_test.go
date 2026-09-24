package payments

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"taro/api/internal/me"
	"taro/api/internal/testutil"
)

func TestE2EInvoiceSnapshotConflictAndTerminalRetry(t *testing.T) {
	r, pg, newUser := testSetup(t)
	tok, uid := newUser()
	var paymentID string
	if err := pg.QueryRow(context.Background(), `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status, idempotency_key)
		SELECT $1, id, 'month_299', 299, 'tg_stars', 'terminal:' || gen_random_uuid(), 299, 199, 'succeeded', 'snapshot-e2e'
		  FROM plans WHERE code='month_299' ORDER BY valid_from DESC LIMIT 1 RETURNING id::text`, uid).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	body := `{"plan_code":"month_299","idempotency_key":"snapshot-e2e"}`
	rec := callPay(r, tok, "POST", "/v1/payments/stars/invoice", body, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("terminal retry: %d %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["payment_id"] != paymentID || got["status"] != "succeeded" {
		t.Fatalf("terminal response: %s", rec.Body.String())
	}
	rec = callPay(r, tok, "POST", "/v1/payments/stars/invoice", `{"plan_code":"year_2490","idempotency_key":"snapshot-e2e"}`, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("snapshot conflict: %d %s", rec.Code, rec.Body.String())
	}
	var conflict map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &conflict)
	if conflict["error"] == nil {
		t.Fatalf("conflict envelope: %s", rec.Body.String())
	}
}

func TestE2EVerifiedLateWebhookGrantsSnapshotEntitlements(t *testing.T) {
	r, pg, newUser := testSetup(t)
	_, uid := newUser()
	if _, err := pg.Exec(context.Background(), `UPDATE users SET tg_id=42 WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	insert := func(code string, price, stars int, key string) string {
		var id string
		if err := pg.QueryRow(ctx, `
			INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status, idempotency_key, created_at)
			SELECT $1, id, $2, $3, 'tg_stars', 'late:' || gen_random_uuid(), $3, $4, 'pending', $5, now() - interval '20 minutes'
			  FROM plans WHERE code=$2 ORDER BY valid_from DESC LIMIT 1 RETURNING id::text`, uid, code, price, stars, key).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	monthID := insert("month_299", 299, 199, "late-month")
	singleID := insert("single_99", 99, 66, "late-single")
	mismatchID := insert("month_299", 299, 199, "late-mismatch")
	svc := New(pg, me.New(pg, nil))
	if n, err := svc.ExpirePending(ctx); err != nil || n != 3 {
		t.Fatalf("expire: n=%d err=%v", n, err)
	}
	t.Setenv("TG_STARS_SECRET_TOKEN", "test-secret")
	call := func(id, code string, amount int, charge, providerCharge string) *httptest.ResponseRecorder {
		body := `{"message":{"from":{"id":42},"successful_payment":{"currency":"XTR","total_amount":` + itoa(amount) + `,"invoice_payload":"` + id + `","telegram_payment_charge_id":"` + charge + `","provider_payment_charge_id":"` + providerCharge + `"}}}`
		return callPay(r, "", "POST", "/v1/payments/stars/webhook", body, map[string]string{"X-Telegram-Bot-Api-Secret-Token": "test-secret"})
	}
	rec := call(monthID, "month_299", 199, "ch_late_month", "pch_late_month")
	if rec.Code != http.StatusOK {
		t.Fatalf("late month: %d %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	if result["late"] != true {
		t.Fatalf("late marker: %s", rec.Body.String())
	}
	rec = call(singleID, "single_99", 66, "ch_late_single", "pch_late_single")
	if rec.Code != http.StatusOK {
		t.Fatalf("late single: %d %s", rec.Code, rec.Body.String())
	}
	rec = call(mismatchID, "month_299", 198, "ch_late_mismatch", "pch_late_mismatch")
	if rec.Code != http.StatusOK {
		t.Fatalf("late mismatch: %d %s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	if result["reconciliation_required"] != true {
		t.Fatalf("late mismatch was not reconciled: %s", rec.Body.String())
	}
	var status string
	if err := pg.QueryRow(ctx, `SELECT status FROM payments WHERE id=$1`, monthID).Scan(&status); err != nil || status != "succeeded" {
		t.Fatalf("month status=%s err=%v", status, err)
	}
	if err := pg.QueryRow(ctx, `SELECT status FROM payments WHERE id=$1`, mismatchID).Scan(&status); err != nil || status != "reconciliation" {
		t.Fatalf("mismatch status=%s err=%v", status, err)
	}
	var linked int
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM subscriptions WHERE payment_id=$1 AND source_type='payment'`, monthID).Scan(&linked); err != nil || linked != 1 {
		t.Fatalf("month entitlement=%d err=%v", linked, err)
	}
	var singleCount int
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM single_entitlements WHERE payment_id=$1`, singleID).Scan(&singleCount); err != nil || singleCount != 1 {
		t.Fatalf("single entitlement=%d err=%v", singleCount, err)
	}
}

func TestE2EWebhookRejectsUnverifiedFields(t *testing.T) {
	r, pg, newUser := testSetup(t)
	_, uid := newUser()
	if _, err := pg.Exec(context.Background(), `UPDATE users SET tg_id=42 WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	var paymentID string
	if err := pg.QueryRow(context.Background(), `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status)
		SELECT $1, id, 'month_299', 299, 'tg_stars', 'validation:' || gen_random_uuid(), 299, 199, 'pending'
		  FROM plans WHERE code='month_299' ORDER BY valid_from DESC LIMIT 1 RETURNING id::text`, uid).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TG_STARS_SECRET_TOKEN", "test-secret")
	cases := []string{
		`{"message":{"successful_payment":{"currency":"XTR","total_amount":199,"invoice_payload":"` + paymentID + `","telegram_payment_charge_id":"ch_missing_owner","provider_payment_charge_id":"pch_missing_owner"}}}`,
		`{"message":{"from":{"id":42},"successful_payment":{"currency":"XTR","total_amount":199,"invoice_payload":"` + paymentID + `","telegram_payment_charge_id":"ch_missing_provider"}}}`,
		`{"message":{"from":{"id":42},"successful_payment":{"currency":"XTR","total_amount":198,"invoice_payload":"` + paymentID + `","telegram_payment_charge_id":"ch_bad_amount","provider_payment_charge_id":"pch_bad_amount"}}}`,
		`{"message":{"from":{"id":43},"successful_payment":{"currency":"XTR","total_amount":199,"invoice_payload":"` + paymentID + `","telegram_payment_charge_id":"ch_bad_owner","provider_payment_charge_id":"pch_bad_owner"}}}`,
	}
	for _, body := range cases {
		rec := callPay(r, "", "POST", "/v1/payments/stars/webhook", body, map[string]string{"X-Telegram-Bot-Api-Secret-Token": "test-secret"})
		if rec.Code != http.StatusOK {
			t.Fatalf("validation response: %d %s", rec.Code, rec.Body.String())
		}
		var result map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &result)
		if result["ignored"] == nil {
			t.Fatalf("accepted invalid webhook: %s", rec.Body.String())
		}
	}
	var status string
	if err := pg.QueryRow(context.Background(), `SELECT status FROM payments WHERE id=$1`, paymentID).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("invalid webhook changed status: %s err=%v", status, err)
	}
}

func TestE2EPaymentSnapshotIsImmutable(t *testing.T) {
	_, pg, newUser := testSetup(t)
	_, uid := newUser()
	var paymentID string
	if err := pg.QueryRow(context.Background(), `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status)
		SELECT $1, id, 'month_299', 299, 'tg_stars', 'immutable:' || gen_random_uuid(), 299, 199, 'pending'
		  FROM plans WHERE code='month_299' ORDER BY valid_from DESC LIMIT 1 RETURNING id::text`, uid).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(context.Background(), `UPDATE payments SET price_rub_snapshot=300 WHERE id=$1`, paymentID); err == nil {
		t.Fatal("payment snapshot must be immutable")
	}
	var price int
	if err := pg.QueryRow(context.Background(), `SELECT price_rub_snapshot FROM payments WHERE id=$1`, paymentID).Scan(&price); err != nil || price != 299 {
		t.Fatalf("snapshot changed: price=%d err=%v", price, err)
	}
}

func TestE2ERefundRevokesOnlyPaymentEntitlements(t *testing.T) {
	pg, do, _, uid := extraSetup(t)
	ctx := context.Background()
	var planID string
	if err := pg.QueryRow(ctx, `SELECT id FROM plans WHERE code='month_299' ORDER BY valid_from DESC LIMIT 1`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	var paymentID, otherPaymentID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status)
		VALUES ($1,$2,'month_299',299,'tg_stars','test:refund-source-' || gen_random_uuid(),299,199,'succeeded') RETURNING id::text`, uid, planID).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status)
		VALUES ($1,$2,'month_299',299,'tg_stars','test:refund-other-' || gen_random_uuid(),299,199,'succeeded') RETURNING id::text`, uid, planID).Scan(&otherPaymentID); err != nil {
		t.Fatal(err)
	}
	var linkedBefore, legacyBefore time.Time
	if err := pg.QueryRow(ctx, `INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, valid_until, payment_id, source_type)
		VALUES ($1,$2,'month_299',299,now()+interval '60 days',$3,'payment') RETURNING valid_until`, uid, planID, paymentID).Scan(&linkedBefore); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, valid_until)
		VALUES ($1,$2,'month_299',299,now()+interval '60 days') RETURNING valid_until`, uid, planID).Scan(&legacyBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `INSERT INTO single_entitlements (user_id, spread_code, payment_id) VALUES ($1,'any',$2),($1,'any',$3)`, uid, paymentID, otherPaymentID); err != nil {
		t.Fatal(err)
	}
	if rec := do("", "POST", "/v1/admin/refund", `{"payment_id":"`+paymentID+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("refund: %d %s", rec.Code, rec.Body.String())
	}
	var status string
	if err := pg.QueryRow(ctx, `SELECT status FROM payments WHERE id=$1`, paymentID).Scan(&status); err != nil || status != "refunded" {
		t.Fatalf("status=%s err=%v", status, err)
	}
	var linkedAfter, legacyAfter time.Time
	if err := pg.QueryRow(ctx, `SELECT valid_until FROM subscriptions WHERE payment_id=$1`, paymentID).Scan(&linkedAfter); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `SELECT valid_until FROM subscriptions WHERE user_id=$1 AND payment_id IS NULL ORDER BY created_at LIMIT 1`, uid).Scan(&legacyAfter); err != nil {
		t.Fatal(err)
	}
	if linkedBefore.Sub(linkedAfter) < 29*24*time.Hour {
		t.Fatalf("payment subscription not revoked: before=%s after=%s", linkedBefore, linkedAfter)
	}
	if legacyAfter.Sub(legacyBefore) > time.Minute || legacyBefore.Sub(legacyAfter) > time.Minute {
		t.Fatalf("legacy subscription changed: before=%s after=%s", legacyBefore, legacyAfter)
	}
	var singleCount int
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM single_entitlements WHERE payment_id=$1`, paymentID).Scan(&singleCount); err != nil || singleCount != 0 {
		t.Fatalf("refunded single=%d err=%v", singleCount, err)
	}
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM single_entitlements WHERE payment_id=$1`, otherPaymentID).Scan(&singleCount); err != nil || singleCount != 1 {
		t.Fatalf("other single changed=%d err=%v", singleCount, err)
	}
}

type refundRoundTripper func(*http.Request) (*http.Response, error)

func (f refundRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestE2ERefundConfirmedFinalizesOnce(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, me.New(pg, rd))
	var calls atomic.Int32
	svc.http = &http.Client{Transport: refundRoundTripper(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`)), Header: make(http.Header)}, nil
	})}
	r := chi.NewRouter()
	r.Post("/v1/admin/refund", svc.HandleRefund)
	uid := testutil.NewUser(t, ctx, pg)
	if _, err := pg.Exec(ctx, `UPDATE users SET tg_id=42 WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	var paymentID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status)
		SELECT $1, id, 'month_299', 299, 'tg_stars', 'tg:ch-confirmed', 299, 199, 'succeeded'
		  FROM plans WHERE code='month_299' ORDER BY valid_from DESC LIMIT 1 RETURNING id::text`, uid).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TG_BOT_TOKEN", "test-token")
	call := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/admin/refund", strings.NewReader(`{"payment_id":"`+paymentID+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if rec := call(); rec.Code != http.StatusOK {
		t.Fatalf("confirmed refund: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(); rec.Code != http.StatusConflict {
		t.Fatalf("confirmed retry: %d %s", rec.Code, rec.Body.String())
	}
	var status string
	if err := pg.QueryRow(ctx, `SELECT status FROM payments WHERE id=$1`, paymentID).Scan(&status); err != nil || status != "refunded" {
		t.Fatalf("confirmed status=%s err=%v", status, err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("confirmed telegram calls=%d", got)
	}
}

func TestE2ERefundAmbiguousRetryDoesNotCallTelegramTwice(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, me.New(pg, rd))
	var calls atomic.Int32
	svc.http = &http.Client{Transport: refundRoundTripper(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("network down")
	})}
	r := chi.NewRouter()
	r.Post("/v1/admin/refund", svc.HandleRefund)
	uid := testutil.NewUser(t, ctx, pg)
	if _, err := pg.Exec(ctx, `UPDATE users SET tg_id=42 WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	var paymentID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status)
		SELECT $1, id, 'month_299', 299, 'tg_stars', 'tg:ch-ambiguous', 299, 199, 'succeeded'
		  FROM plans WHERE code='month_299' ORDER BY valid_from DESC LIMIT 1 RETURNING id::text`, uid).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TG_BOT_TOKEN", "test-token")
	call := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/admin/refund", strings.NewReader(`{"payment_id":"`+paymentID+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if rec := call(); rec.Code != http.StatusBadGateway {
		t.Fatalf("first refund: %d %s", rec.Code, rec.Body.String())
	}
	var status, state string
	if err := pg.QueryRow(ctx, `SELECT status, refund_state FROM payments WHERE id=$1`, paymentID).Scan(&status, &state); err != nil {
		t.Fatal(err)
	}
	if status != "refunding" || state != "unknown" {
		t.Fatalf("ambiguous state: %s/%s", status, state)
	}
	if rec := call(); rec.Code != http.StatusConflict {
		t.Fatalf("retry refund: %d %s", rec.Code, rec.Body.String())
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("telegram calls=%d", got)
	}
}
