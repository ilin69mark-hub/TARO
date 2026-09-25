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
	var status, reason string
	if err := pg.QueryRow(ctx, `SELECT status FROM payments WHERE id=$1`, monthID).Scan(&status); err != nil || status != "succeeded" {
		t.Fatalf("month status=%s err=%v", status, err)
	}
	if err := pg.QueryRow(ctx, `SELECT status, COALESCE(reconciliation_reason,'') FROM payments WHERE id=$1`, mismatchID).Scan(&status, &reason); err != nil || status != "expired" || reason != "amount_mismatch" {
		t.Fatalf("mismatch state=%s/%s err=%v", status, reason, err)
	}
	rec = call(mismatchID, "month_299", 199, "ch_late_mismatch_valid", "pch_late_mismatch_valid")
	if rec.Code != http.StatusOK {
		t.Fatalf("mismatch recovery: %d %s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	if result["late"] != true {
		t.Fatalf("mismatch recovery marker: %s", rec.Body.String())
	}
	if err := pg.QueryRow(ctx, `SELECT status, COALESCE(reconciliation_reason,'') FROM payments WHERE id=$1`, mismatchID).Scan(&status, &reason); err != nil || status != "succeeded" || reason != "" {
		t.Fatalf("recovered mismatch state=%s/%s err=%v", status, reason, err)
	}
	var recovered int
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM subscriptions WHERE payment_id=$1 AND source_type='payment'`, mismatchID).Scan(&recovered); err != nil || recovered != 1 {
		t.Fatalf("recovered entitlement=%d err=%v", recovered, err)
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

func TestE2EWebhookDurablyRecordsPendingMismatches(t *testing.T) {
	r, pg, newUser := testSetup(t)
	t.Setenv("TG_STARS_SECRET_TOKEN", "test-secret")
	cases := []struct {
		name             string
		amount           int
		accountOwner     int64
		webhookOwner     int64
		existingTG       string
		existingProvider string
		reason           string
	}{
		{name: "amount", amount: 198, accountOwner: 4201, webhookOwner: 4201, reason: "amount_mismatch"},
		{name: "owner", amount: 199, accountOwner: 4202, webhookOwner: 4203, reason: "owner_mismatch"},
		{name: "charge", amount: 199, accountOwner: 4203, webhookOwner: 4203, existingTG: "ch_existing_charge", existingProvider: "pch_existing_charge", reason: "charge_mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, uid := newUser()
			if _, err := pg.Exec(context.Background(), `UPDATE users SET tg_id=$1 WHERE id=$2`, tc.accountOwner, uid); err != nil {
				t.Fatal(err)
			}
			var paymentID string
			if err := pg.QueryRow(context.Background(), `
				INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status, idempotency_key, telegram_payment_charge_id, provider_payment_charge_id)
				SELECT $1, id, 'month_299', 299, 'tg_stars', 'pending:' || gen_random_uuid(), 299, 199, 'pending', $2, NULLIF($3, ''), NULLIF($4, '')
				  FROM plans WHERE code='month_299' ORDER BY valid_from DESC LIMIT 1 RETURNING id::text`, uid, "mismatch-"+tc.name, tc.existingTG, tc.existingProvider).Scan(&paymentID); err != nil {
				t.Fatal(err)
			}
			body := `{"message":{"from":{"id":` + itoa(int(tc.webhookOwner)) + `},"successful_payment":{"currency":"XTR","total_amount":` + itoa(tc.amount) + `,"invoice_payload":"` + paymentID + `","telegram_payment_charge_id":"ch_` + tc.name + `","provider_payment_charge_id":"pch_` + tc.name + `"}}}`
			headers := map[string]string{"X-Telegram-Bot-Api-Secret-Token": "test-secret"}
			rec := callPay(r, "", "POST", "/v1/payments/stars/webhook", body, headers)
			if rec.Code != http.StatusOK {
				t.Fatalf("mismatch response: %d %s", rec.Code, rec.Body.String())
			}
			var result map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || result["reconciliation_required"] != true {
				t.Fatalf("mismatch result: %s", rec.Body.String())
			}
			var status, reason, note string
			if err := pg.QueryRow(context.Background(), `SELECT status, COALESCE(reconciliation_reason,''), note FROM payments WHERE id=$1`, paymentID).Scan(&status, &reason, &note); err != nil {
				t.Fatal(err)
			}
			if status != "pending" || reason != tc.reason || note == "" {
				t.Fatalf("mismatch state=%s/%s note=%q", status, reason, note)
			}
			var events, entitlements int
			if err := pg.QueryRow(context.Background(), `SELECT COUNT(*) FROM payment_webhook_events WHERE payment_id=$1`, paymentID).Scan(&events); err != nil {
				t.Fatal(err)
			}
			if err := pg.QueryRow(context.Background(), `SELECT COUNT(*) FROM subscriptions WHERE payment_id=$1`, paymentID).Scan(&entitlements); err != nil {
				t.Fatal(err)
			}
			if events != 1 || entitlements != 0 {
				t.Fatalf("mismatch audit=%d entitlements=%d", events, entitlements)
			}
			rec = callPay(r, "", "POST", "/v1/payments/stars/webhook", body, headers)
			if rec.Code != http.StatusOK {
				t.Fatalf("mismatch retry: %d %s", rec.Code, rec.Body.String())
			}
			if err := pg.QueryRow(context.Background(), `SELECT COUNT(*) FROM payment_webhook_events WHERE payment_id=$1`, paymentID).Scan(&events); err != nil {
				t.Fatal(err)
			}
			if events != 1 {
				t.Fatalf("mismatch retry audit=%d", events)
			}
			validCharge := "ch_" + tc.name + "_valid"
			validProviderCharge := "pch_" + tc.name + "_valid"
			if tc.existingTG != "" {
				validCharge = tc.existingTG
				validProviderCharge = tc.existingProvider
			}
			validBody := `{"message":{"from":{"id":` + itoa(int(tc.accountOwner)) + `},"successful_payment":{"currency":"XTR","total_amount":199,"invoice_payload":"` + paymentID + `","telegram_payment_charge_id":"` + validCharge + `","provider_payment_charge_id":"` + validProviderCharge + `"}}}`
			rec = callPay(r, "", "POST", "/v1/payments/stars/webhook", validBody, headers)
			if rec.Code != http.StatusOK {
				t.Fatalf("valid recovery: %d %s", rec.Code, rec.Body.String())
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &result)
			if result["ok"] != true || result["late"] == true {
				t.Fatalf("valid recovery result: %s", rec.Body.String())
			}
			if err := pg.QueryRow(context.Background(), `SELECT status, COALESCE(reconciliation_reason,'') FROM payments WHERE id=$1`, paymentID).Scan(&status, &reason); err != nil || status != "succeeded" || reason != "" {
				t.Fatalf("recovered state=%s/%s err=%v", status, reason, err)
			}
			if err := pg.QueryRow(context.Background(), `SELECT COUNT(*) FROM subscriptions WHERE payment_id=$1`, paymentID).Scan(&entitlements); err != nil || entitlements != 1 {
				t.Fatalf("recovered entitlement=%d err=%v", entitlements, err)
			}
		})
	}
}

func TestE2EWebhookOwnerUnverifiedStaysBlocked(t *testing.T) {
	r, pg, newUser := testSetup(t)
	t.Setenv("TG_STARS_SECRET_TOKEN", "test-secret")
	_, uid := newUser()
	var paymentID string
	if err := pg.QueryRow(context.Background(), `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status, idempotency_key)
		SELECT $1, id, 'month_299', 299, 'tg_stars', 'owner-unverified:' || gen_random_uuid(), 299, 199, 'pending', 'owner-unverified-e2e'
		  FROM plans WHERE code='month_299' ORDER BY valid_from DESC LIMIT 1 RETURNING id::text`, uid).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	body := `{"message":{"from":{"id":42},"successful_payment":{"currency":"XTR","total_amount":199,"invoice_payload":"` + paymentID + `","telegram_payment_charge_id":"ch_owner_unverified","provider_payment_charge_id":"pch_owner_unverified"}}}`
	headers := map[string]string{"X-Telegram-Bot-Api-Secret-Token": "test-secret"}
	rec := callPay(r, "", "POST", "/v1/payments/stars/webhook", body, headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner-unverified response: %d %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || result["reconciliation_required"] != true {
		t.Fatalf("owner-unverified result: %s", rec.Body.String())
	}
	var status, reason string
	if err := pg.QueryRow(context.Background(), `SELECT status, COALESCE(reconciliation_reason,'') FROM payments WHERE id=$1`, paymentID).Scan(&status, &reason); err != nil || status != "reconciliation" || reason != "owner_unverified" {
		t.Fatalf("owner-unverified state=%s/%s err=%v", status, reason, err)
	}
	var entitlements int
	if err := pg.QueryRow(context.Background(), `SELECT COUNT(*) FROM subscriptions WHERE payment_id=$1`, paymentID).Scan(&entitlements); err != nil || entitlements != 0 {
		t.Fatalf("owner-unverified entitlement=%d err=%v", entitlements, err)
	}
}

func TestE2EWebhookOwnerUnverifiedOwnerSwapCannotRecover(t *testing.T) {
	r, pg, newUser := testSetup(t)
	t.Setenv("TG_STARS_SECRET_TOKEN", "test-secret")
	_, uid := newUser()
	var paymentID string
	if err := pg.QueryRow(context.Background(), `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status, idempotency_key)
		SELECT $1, id, 'month_299', 299, 'tg_stars', 'owner-swap:' || gen_random_uuid(), 299, 199, 'pending', 'owner-swap-e2e'
		  FROM plans WHERE code='month_299' ORDER BY valid_from DESC LIMIT 1 RETURNING id::text`, uid).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	call := func(owner int64, charge, providerCharge string) *httptest.ResponseRecorder {
		body := `{"message":{"from":{"id":` + itoa(int(owner)) + `},"successful_payment":{"currency":"XTR","total_amount":199,"invoice_payload":"` + paymentID + `","telegram_payment_charge_id":"` + charge + `","provider_payment_charge_id":"` + providerCharge + `"}}}`
		return callPay(r, "", "POST", "/v1/payments/stars/webhook", body, map[string]string{"X-Telegram-Bot-Api-Secret-Token": "test-secret"})
	}
	if rec := call(42, "ch_owner_swap", "pch_owner_swap"); rec.Code != http.StatusOK {
		t.Fatalf("initial owner-unverified response: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := pg.Exec(context.Background(), `UPDATE users SET tg_id=43 WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	rec := call(43, "ch_owner_swap", "pch_owner_swap")
	if rec.Code != http.StatusOK {
		t.Fatalf("owner-swap response: %d %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || result["reconciliation_required"] != true {
		t.Fatalf("owner-swap result: %s", rec.Body.String())
	}
	var status, reason string
	if err := pg.QueryRow(context.Background(), `SELECT status, COALESCE(reconciliation_reason,'') FROM payments WHERE id=$1`, paymentID).Scan(&status, &reason); err != nil || status != "reconciliation" || reason != "owner_unverified" {
		t.Fatalf("owner-swap state=%s/%s err=%v", status, reason, err)
	}
	var entitlements int
	if err := pg.QueryRow(context.Background(), `SELECT COUNT(*) FROM subscriptions WHERE payment_id=$1`, paymentID).Scan(&entitlements); err != nil || entitlements != 0 {
		t.Fatalf("owner-swap entitlement=%d err=%v", entitlements, err)
	}
	var originalOwner int64
	var originalTelegramCharge, originalProviderCharge string
	if err := pg.QueryRow(context.Background(), `
		SELECT owner_tg_id, telegram_charge_id, provider_charge_id
		FROM payment_webhook_events WHERE payment_id=$1 AND reason='owner_unverified'
		ORDER BY created_at, id LIMIT 1`, paymentID).Scan(&originalOwner, &originalTelegramCharge, &originalProviderCharge); err != nil {
		t.Fatal(err)
	}
	if originalOwner != 42 || originalTelegramCharge != "ch_owner_swap" || originalProviderCharge != "pch_owner_swap" {
		t.Fatalf("owner identity=%d/%s/%s", originalOwner, originalTelegramCharge, originalProviderCharge)
	}
}

func TestE2EWebhookOwnerUnverifiedReusedChargeStaysManual(t *testing.T) {
	r, pg, newUser := testSetup(t)
	t.Setenv("TG_STARS_SECRET_TOKEN", "test-secret")
	_, targetUID := newUser()
	_, otherUID := newUser()
	insert := func(uid, key string) string {
		var id string
		if err := pg.QueryRow(context.Background(), `
			INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status, idempotency_key)
			SELECT $1, id, 'month_299', 299, 'tg_stars', 'owner-reuse:' || gen_random_uuid(), 299, 199, 'pending', $2
			  FROM plans WHERE code='month_299' ORDER BY valid_from DESC LIMIT 1 RETURNING id::text`, uid, key).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	otherID := insert(otherUID, "owner-reuse-other")
	targetID := insert(targetUID, "owner-reuse-target")
	if _, err := pg.Exec(context.Background(), `
		UPDATE payments SET telegram_payment_charge_id='ch_owner_reused',
		provider_payment_charge_id='pch_owner_reused' WHERE id=$1`, otherID); err != nil {
		t.Fatal(err)
	}
	call := func(id string, charge, providerCharge string) *httptest.ResponseRecorder {
		body := `{"message":{"from":{"id":42},"successful_payment":{"currency":"XTR","total_amount":199,"invoice_payload":"` + id + `","telegram_payment_charge_id":"` + charge + `","provider_payment_charge_id":"` + providerCharge + `"}}}`
		return callPay(r, "", "POST", "/v1/payments/stars/webhook", body, map[string]string{"X-Telegram-Bot-Api-Secret-Token": "test-secret"})
	}
	if rec := call(targetID, "ch_owner_reused", "pch_owner_reused"); rec.Code != http.StatusOK {
		t.Fatalf("reused owner-unverified response: %d %s", rec.Code, rec.Body.String())
	}
	var status, reason string
	if err := pg.QueryRow(context.Background(), `SELECT status, COALESCE(reconciliation_reason,'') FROM payments WHERE id=$1`, targetID).Scan(&status, &reason); err != nil || status != "reconciliation" || reason != "charge_reused" {
		t.Fatalf("reused owner-unverified state=%s/%s err=%v", status, reason, err)
	}
	if _, err := pg.Exec(context.Background(), `UPDATE users SET tg_id=42 WHERE id=$1`, targetUID); err != nil {
		t.Fatal(err)
	}
	if rec := call(targetID, "ch_owner_reused_other", "pch_owner_reused_other"); rec.Code != http.StatusOK {
		t.Fatalf("different reused-charge response: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(targetID, "ch_owner_reused", "pch_owner_reused"); rec.Code != http.StatusOK {
		t.Fatalf("same reused-charge response: %d %s", rec.Code, rec.Body.String())
	}
	if err := pg.QueryRow(context.Background(), `SELECT status, COALESCE(reconciliation_reason,'') FROM payments WHERE id=$1`, targetID).Scan(&status, &reason); err != nil || status != "reconciliation" || reason != "charge_reused" {
		t.Fatalf("reused owner-unverified final state=%s/%s err=%v", status, reason, err)
	}
	var entitlements, originalEvents, reuseEvents int
	if err := pg.QueryRow(context.Background(), `SELECT COUNT(*) FROM subscriptions WHERE payment_id=$1`, targetID).Scan(&entitlements); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(context.Background(), `SELECT COUNT(*) FROM payment_webhook_events WHERE payment_id=$1 AND reason='owner_unverified'`, targetID).Scan(&originalEvents); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(context.Background(), `SELECT COUNT(*) FROM payment_webhook_events WHERE payment_id=$1 AND reason='charge_reused'`, targetID).Scan(&reuseEvents); err != nil {
		t.Fatal(err)
	}
	if entitlements != 0 || originalEvents != 1 || reuseEvents != 1 {
		t.Fatalf("reused owner-unverified audit entitlements=%d original=%d reuse=%d", entitlements, originalEvents, reuseEvents)
	}
}

func TestE2EWebhookReusedChargeMismatchCanRecover(t *testing.T) {
	r, pg, newUser := testSetup(t)
	t.Setenv("TG_STARS_SECRET_TOKEN", "test-secret")
	_, uid := newUser()
	if _, err := pg.Exec(context.Background(), `UPDATE users SET tg_id=42 WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	insert := func(key string) string {
		var id string
		if err := pg.QueryRow(context.Background(), `
			INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status, idempotency_key)
			SELECT $1, id, 'month_299', 299, 'tg_stars', 'reuse:' || gen_random_uuid(), 299, 199, 'pending', $2
			  FROM plans WHERE code='month_299' ORDER BY valid_from DESC LIMIT 1 RETURNING id::text`, uid, key).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	firstID := insert("reuse-first")
	secondID := insert("reuse-second")
	if _, err := pg.Exec(context.Background(), `UPDATE payments SET telegram_payment_charge_id='ch_reused_original', provider_payment_charge_id='pch_reused_original' WHERE id=$1`, firstID); err != nil {
		t.Fatal(err)
	}
	call := func(id, charge, providerCharge string) *httptest.ResponseRecorder {
		body := `{"message":{"from":{"id":42},"successful_payment":{"currency":"XTR","total_amount":199,"invoice_payload":"` + id + `","telegram_payment_charge_id":"` + charge + `","provider_payment_charge_id":"` + providerCharge + `"}}}`
		return callPay(r, "", "POST", "/v1/payments/stars/webhook", body, map[string]string{"X-Telegram-Bot-Api-Secret-Token": "test-secret"})
	}
	rec := call(secondID, "ch_reused_original", "pch_reused_original")
	if rec.Code != http.StatusOK {
		t.Fatalf("reused mismatch: %d %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	if result["reconciliation_required"] != true {
		t.Fatalf("reused mismatch result: %s", rec.Body.String())
	}
	var status, reason string
	if err := pg.QueryRow(context.Background(), `SELECT status, COALESCE(reconciliation_reason,'') FROM payments WHERE id=$1`, secondID).Scan(&status, &reason); err != nil || status != "pending" || reason != "charge_reused" {
		t.Fatalf("reused state=%s/%s err=%v", status, reason, err)
	}
	rec = call(secondID, "ch_reuse_valid", "pch_reuse_valid")
	if rec.Code != http.StatusOK {
		t.Fatalf("reused recovery: %d %s", rec.Code, rec.Body.String())
	}
	if err := pg.QueryRow(context.Background(), `SELECT status, COALESCE(reconciliation_reason,'') FROM payments WHERE id=$1`, secondID).Scan(&status, &reason); err != nil || status != "succeeded" || reason != "" {
		t.Fatalf("reused recovered state=%s/%s err=%v", status, reason, err)
	}
	var audits, entitlements int
	if err := pg.QueryRow(context.Background(), `SELECT COUNT(*) FROM payment_webhook_events WHERE payment_id=$1 AND reason='charge_reused'`, secondID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(context.Background(), `SELECT COUNT(*) FROM subscriptions WHERE payment_id=$1`, secondID).Scan(&entitlements); err != nil {
		t.Fatal(err)
	}
	if audits != 1 || entitlements != 1 {
		t.Fatalf("reused recovery audit=%d entitlement=%d", audits, entitlements)
	}
}

func TestE2EWebhookDuplicateChargeIsDurableAndIdempotent(t *testing.T) {
	r, pg, newUser := testSetup(t)
	t.Setenv("TG_STARS_SECRET_TOKEN", "test-secret")
	_, uid := newUser()
	if _, err := pg.Exec(context.Background(), `UPDATE users SET tg_id=42 WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	var paymentID string
	if err := pg.QueryRow(context.Background(), `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status, idempotency_key)
		SELECT $1, id, 'month_299', 299, 'tg_stars', 'pending:' || gen_random_uuid(), 299, 199, 'pending', 'duplicate-charge-e2e'
		  FROM plans WHERE code='month_299' ORDER BY valid_from DESC LIMIT 1 RETURNING id::text`, uid).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	call := func(charge, providerCharge string) *httptest.ResponseRecorder {
		body := `{"message":{"from":{"id":42},"successful_payment":{"currency":"XTR","total_amount":199,"invoice_payload":"` + paymentID + `","telegram_payment_charge_id":"` + charge + `","provider_payment_charge_id":"` + providerCharge + `"}}}`
		return callPay(r, "", "POST", "/v1/payments/stars/webhook", body, map[string]string{"X-Telegram-Bot-Api-Secret-Token": "test-secret"})
	}
	if rec := call("ch_original", "pch_original"); rec.Code != http.StatusOK {
		t.Fatalf("initial webhook: %d %s", rec.Code, rec.Body.String())
	}
	for i := 0; i < 2; i++ {
		rec := call("ch_duplicate", "pch_duplicate")
		if rec.Code != http.StatusOK {
			t.Fatalf("duplicate webhook: %d %s", rec.Code, rec.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || result["duplicate"] != true {
			t.Fatalf("duplicate result: %s", rec.Body.String())
		}
	}
	var status, reason string
	if err := pg.QueryRow(context.Background(), `SELECT status, reconciliation_reason FROM payments WHERE id=$1`, paymentID).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || reason != "duplicate_charge" {
		t.Fatalf("duplicate state=%s/%s", status, reason)
	}
	var events, subscriptions int
	if err := pg.QueryRow(context.Background(), `SELECT COUNT(*) FROM payment_webhook_events WHERE payment_id=$1 AND reason='duplicate_charge'`, paymentID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(context.Background(), `SELECT COUNT(*) FROM subscriptions WHERE payment_id=$1`, paymentID).Scan(&subscriptions); err != nil {
		t.Fatal(err)
	}
	if events != 1 || subscriptions != 1 {
		t.Fatalf("duplicate audit=%d subscriptions=%d", events, subscriptions)
	}
}

func TestE2EWebhookRecoversExistingReconciliation(t *testing.T) {
	r, pg, newUser := testSetup(t)
	t.Setenv("TG_STARS_SECRET_TOKEN", "test-secret")
	_, uid := newUser()
	if _, err := pg.Exec(context.Background(), `UPDATE users SET tg_id=42 WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	var paymentID string
	if err := pg.QueryRow(context.Background(), `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status, idempotency_key, reconciliation_reason)
		SELECT $1, id, 'month_299', 299, 'tg_stars', 'legacy-reconcile:' || gen_random_uuid(), 299, 199, 'reconciliation', 'legacy-reconcile-e2e', 'amount_mismatch'
		  FROM plans WHERE code='month_299' ORDER BY valid_from DESC LIMIT 1 RETURNING id::text`, uid).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	body := `{"message":{"from":{"id":42},"successful_payment":{"currency":"XTR","total_amount":199,"invoice_payload":"` + paymentID + `","telegram_payment_charge_id":"ch_legacy_valid","provider_payment_charge_id":"pch_legacy_valid"}}}`
	rec := callPay(r, "", "POST", "/v1/payments/stars/webhook", body, map[string]string{"X-Telegram-Bot-Api-Secret-Token": "test-secret"})
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy reconciliation: %d %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	if result["late"] != true {
		t.Fatalf("legacy reconciliation result: %s", rec.Body.String())
	}
	var status, reason string
	if err := pg.QueryRow(context.Background(), `SELECT status, COALESCE(reconciliation_reason,'') FROM payments WHERE id=$1`, paymentID).Scan(&status, &reason); err != nil || status != "succeeded" || reason != "" {
		t.Fatalf("legacy recovered state=%s/%s err=%v", status, reason, err)
	}
	var entitlements int
	if err := pg.QueryRow(context.Background(), `SELECT COUNT(*) FROM subscriptions WHERE payment_id=$1`, paymentID).Scan(&entitlements); err != nil || entitlements != 1 {
		t.Fatalf("legacy entitlement=%d err=%v", entitlements, err)
	}
}

func TestE2EWebhookAuditSurvivesPaymentDeletion(t *testing.T) {
	_, pg, newUser := testSetup(t)
	_, uid := newUser()
	var paymentID string
	if err := pg.QueryRow(context.Background(), `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status, idempotency_key)
		SELECT $1, id, 'month_299', 299, 'tg_stars', 'audit-delete:' || gen_random_uuid(), 299, 199, 'pending', 'audit-delete-e2e'
		  FROM plans WHERE code='month_299' ORDER BY valid_from DESC LIMIT 1 RETURNING id::text`, uid).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	sp := successfulPayment{Currency: "XTR", TotalAmount: 199, InvoicePayload: paymentID, TelegramPaymentCharge: "ch_audit_delete", ProviderPaymentCharge: "pch_audit_delete"}
	if _, err := pg.Exec(context.Background(), `
		INSERT INTO payment_webhook_events (payment_id, event_hash, reason, currency, total_amount, telegram_charge_id, provider_charge_id, owner_tg_id)
		VALUES ($1,$2,'amount_mismatch','XTR',199,'ch_audit_delete','pch_audit_delete',42)`, paymentID, webhookEventHash(sp, 42)); err != nil {
		t.Fatal(err)
	}
	var cascades bool
	if err := pg.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM pg_constraint
			WHERE conrelid='payment_webhook_events'::regclass AND contype='f' AND confdeltype='c'
		)`).Scan(&cascades); err != nil {
		t.Fatal(err)
	}
	if cascades {
		t.Skip("029 is already applied with cascading payment_webhook_events foreign key")
	}
	if _, err := pg.Exec(context.Background(), `DELETE FROM payments WHERE id=$1`, paymentID); err != nil {
		t.Fatal(err)
	}
	var auditPaymentID, telegramCharge, providerCharge string
	var ownerTG int64
	if err := pg.QueryRow(context.Background(), `
		SELECT payment_id::text, telegram_charge_id, provider_charge_id, owner_tg_id
		FROM payment_webhook_events WHERE payment_id=$1`, paymentID).Scan(&auditPaymentID, &telegramCharge, &providerCharge, &ownerTG); err != nil {
		t.Fatal(err)
	}
	if auditPaymentID != paymentID || telegramCharge != "ch_audit_delete" || providerCharge != "pch_audit_delete" || ownerTG != 42 {
		t.Fatalf("audit identity=%s/%s/%s/%d", auditPaymentID, telegramCharge, providerCharge, ownerTG)
	}
}

func TestE2EPaymentSnapshotIsImmutable(t *testing.T) {
	_, pg, newUser := testSetup(t)
	_, uid := newUser()
	var paymentID string
	if err := pg.QueryRow(context.Background(), `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status, idempotency_key)
		SELECT $1, id, 'month_299', 299, 'tg_stars', 'immutable:' || gen_random_uuid(), 299, 199, 'pending', 'immutable-snapshot-e2e'
		  FROM plans WHERE code='month_299' ORDER BY valid_from DESC LIMIT 1 RETURNING id::text`, uid).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	var originalPlanID string
	if err := pg.QueryRow(context.Background(), `SELECT plan_id::text FROM payments WHERE id=$1`, paymentID).Scan(&originalPlanID); err != nil {
		t.Fatal(err)
	}
	updates := []string{
		`UPDATE payments SET plan_id=(SELECT id FROM plans WHERE code='year_2490' ORDER BY valid_from DESC LIMIT 1) WHERE id=$1`,
		`UPDATE payments SET amount_rub=300 WHERE id=$1`,
		`UPDATE payments SET idempotency_key='changed-snapshot' WHERE id=$1`,
	}
	for _, update := range updates {
		if _, err := pg.Exec(context.Background(), update, paymentID); err == nil {
			t.Fatalf("payment snapshot update was accepted: %s", update)
		}
	}
	var planID string
	var price, amount int
	var key string
	if err := pg.QueryRow(context.Background(), `SELECT plan_id::text, price_rub_snapshot, amount_rub, idempotency_key FROM payments WHERE id=$1`, paymentID).Scan(&planID, &price, &amount, &key); err != nil {
		t.Fatal(err)
	}
	if planID != originalPlanID || price != 299 || amount != 299 || key != "immutable-snapshot-e2e" {
		t.Fatalf("snapshot changed: plan=%s price=%d amount=%d key=%s", planID, price, amount, key)
	}
}

func TestE2EPaymentConstraintsAreValidated(t *testing.T) {
	_, pg, _ := testSetup(t)
	constraints := []struct {
		table string
		name  string
	}{
		{table: "payments", name: "payments_idempotency_key_check"},
		{table: "payments", name: "payments_amounts_check"},
		{table: "payments", name: "payments_duration_snapshot_check"},
		{table: "payments", name: "payments_refund_state_transition_check"},
		{table: "subscriptions", name: "subscriptions_price_snapshot_check"},
	}
	for _, constraint := range constraints {
		var validated bool
		if err := pg.QueryRow(context.Background(), `SELECT convalidated FROM pg_constraint WHERE conrelid=$1::regclass AND conname=$2`, constraint.table, constraint.name).Scan(&validated); err != nil {
			t.Fatal(err)
		}
		if !validated {
			t.Fatalf("constraint %s is not validated", constraint.name)
		}
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
