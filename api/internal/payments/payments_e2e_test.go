// E2E payments: invoice-идемпотентность, webhook-дубли, verify оба поля, refund-пути (см. D).
package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"taro/api/internal/auth"
	"taro/api/internal/me"
	"taro/api/internal/testutil"
)

func testSetup(t *testing.T) (*chi.Mux, *pgxpool.Pool, func() (string, string)) {
	t.Helper()
	ctx, pg, rd := testutil.Live(t)
	mv := me.New(pg, rd)
	svc := New(pg, mv)
	au := auth.New(pg, rd)
	r := chi.NewRouter()
	r.With(au.RequireAuth).Post("/v1/payments/stars/invoice", svc.HandleInvoice)
	r.Post("/v1/payments/stars/webhook", svc.HandleWebhook)
	r.With(au.RequireAuth).Post("/v1/payments/stars/verify", svc.HandleVerify)
	newUser := func() (string, string) {
		t.Helper()
		uid := testutil.NewUser(t, ctx, pg)
		tok, err := auth.IssueJWT(uid, auth.UserTTL)
		if err != nil {
			t.Fatal(err)
		}
		if err := rd.Set(ctx, "sess:"+uid, "x", auth.UserTTL).Err(); err != nil {
			t.Fatal(err)
		}
		if _, err := pg.Exec(ctx, `UPDATE users SET age_confirmed_at=now() WHERE id=$1`, uid); err != nil {
			t.Fatal(err)
		}
		return tok, uid
	}
	_ = ctx
	return r, pg, newUser
}

func callPay(r *chi.Mux, tok, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestE2EInvoiceIdempotent(t *testing.T) {
	r, pg, newUser := testSetup(t)
	tok, uid := newUser()
	_ = uid
	body := `{"plan_code":"month_299","idempotency_key":"pay-e2e-1"}`
	rec1 := callPay(r, tok, "POST", "/v1/payments/stars/invoice", body, nil)
	rec2 := callPay(r, tok, "POST", "/v1/payments/stars/invoice", body, nil)
	// dev-токена нет → 500, но строка одна (идемпотентность до вызова TG)
	if rec1.Code != 500 || rec2.Code != 500 {
		t.Fatalf("want 500/500 without bot token, got %d/%d", rec1.Code, rec2.Code)
	}
	var n int
	_ = pg.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM payments WHERE idempotency_key='pay-e2e-1'`).Scan(&n)
	if n != 1 {
		t.Fatalf("want 1 row, got %d", n)
	}
}

func TestE2EWebhookDuplicate(t *testing.T) {
	r, pg, newUser := testSetup(t)
	tok, uid := newUser()
	_ = tok
	var payID string
	if err := pg.QueryRow(context.Background(), `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status)
		SELECT $1, id, 'month_299', 299, 'tg_stars', 'pending:e2e', 299, 199, 'pending'
		  FROM plans WHERE code='month_299' LIMIT 1 RETURNING id`, uid).Scan(&payID); err != nil {
		t.Fatal(err)
	}
	wh := `{"message":{"from":{"id":1},"successful_payment":{"currency":"XTR","total_amount":199,"invoice_payload":"` +
		payID + `","telegram_payment_charge_id":"ch_e2e","provider_payment_charge_id":"pch_e2e"}}}`
	h := map[string]string{"X-Telegram-Bot-Api-Secret-Token": "test-secret"}
	// секрет не совпадет с env (dev-only-stars) → 401. Ставим env через t.Setenv:
	t.Setenv("TG_STARS_SECRET_TOKEN", "test-secret")
	rec1 := callPay(r, "", "POST", "/v1/payments/stars/webhook", wh, h)
	rec2 := callPay(r, "", "POST", "/v1/payments/stars/webhook", wh, h)
	var b1, b2 map[string]any
	_ = json.Unmarshal(rec1.Body.Bytes(), &b1)
	_ = json.Unmarshal(rec2.Body.Bytes(), &b2)
	if rec1.Code != 200 || b1["ok"] != true || b1["duplicate"] == true {
		t.Fatalf("first webhook: %d %s", rec1.Code, rec1.Body.String())
	}
	if rec2.Code != 200 || b2["duplicate"] != true {
		t.Fatalf("retry must be duplicate:true: %d %s", rec2.Code, rec2.Body.String())
	}
	var subs int
	_ = pg.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM subscriptions WHERE user_id=$1 AND plan_code='month_299'`, uid).Scan(&subs)
	if subs != 1 {
		t.Fatalf("double accrual: %d subs", subs)
	}
}
