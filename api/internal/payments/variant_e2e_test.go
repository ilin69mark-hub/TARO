// E2E payments-довесок: variant, winback+note, verify, expire (см. D-покрытие).
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

func paySetup(t *testing.T) (*pgxpool.Pool, func(method, path, body string) *httptest.ResponseRecorder, string) {
	t.Helper()
	ctx, pg, rd := testutil.Live(t)
	mv := me.New(pg, rd)
	svc := New(pg, mv)
	au := auth.New(pg, rd)
	r := chi.NewRouter()
	r.With(au.RequireAuth).Post("/v1/payments/stars/invoice", svc.HandleInvoice)
	r.Post("/v1/payments/stars/webhook", svc.HandleWebhook)
	r.With(au.RequireAuth).Post("/v1/payments/stars/verify", svc.HandleVerify)
	r.With(au.RequireAuth).Get("/v1/ab/me", svc.HandleVariant)
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
	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	return pg, do, uid
}

func TestE2EVariantWinback(t *testing.T) {
	pg, do, uid := paySetup(t)
	ctx := context.Background()

	// без конфига → control/0
	var v map[string]any
	if rec := do("GET", "/v1/ab/me", ""); rec.Code != 200 {
		t.Fatalf("ab: %d", rec.Code)
	} else if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || v["variant"] != "control" {
		t.Fatalf("ab off: %s", rec.Body.String())
	}

	// split=100 → test/349
	if _, err := pg.Exec(ctx, `INSERT INTO app_config (key, value)
		VALUES ('ab.price_month','{"enabled":true,"control":299,"test":349,"split":100}')
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value`); err != nil {
		t.Fatal(err)
	}
	defer pg.Exec(context.Background(), `DELETE FROM app_config WHERE key='ab.price_month'`)
	rec := do("GET", "/v1/ab/me", "")
	var v2 map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &v2)
	if v2["variant"] != "test" || v2["price_rub"] != float64(349) {
		t.Fatalf("ab on: %s", rec.Body.String())
	}

	// протухшая подписка 20д + winback 20% → invoice 239 + note
	if _, err := pg.Exec(ctx, `
		INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, valid_until, status)
		SELECT $1, id, 'month_299', 299, now() - interval '20 days', 'expired'
		  FROM plans WHERE code='month_299' LIMIT 1`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `INSERT INTO app_config (key, value)
		VALUES ('offers.winback','{"enabled":true,"pct":20}')
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value`); err != nil {
		t.Fatal(err)
	}
	defer pg.Exec(context.Background(), `DELETE FROM app_config WHERE key='offers.winback'`)
	// invoice без bot-ключа → 500, но строка с winback-ценой создана
	if rec := do("POST", "/v1/payments/stars/invoice", `{"plan_code":"month_299","idempotency_key":"winback-e2e-1"}`); rec.Code != 500 {
		t.Fatalf("invoice dev: want 500 got %d", rec.Code)
	}
	var price int
	var note *string
	if err := pg.QueryRow(ctx,
		`SELECT price_rub_snapshot, note FROM payments WHERE user_id=$1 ORDER BY created_at DESC LIMIT 1`,
		uid).Scan(&price, &note); err != nil {
		t.Fatal(err)
	}
	// A/B test=349 применился первым, затем winback −20%: 349*0.8=279 (см. U22+V16)
	if price != 279 || note == nil || *note != "winback-20" {
		t.Fatalf("winback: price=%d note=%v", price, note)
	}

	// verify по provider_payment_id (спека D3)
	var ppc string
	_ = pg.QueryRow(ctx,
		`SELECT provider_payment_id FROM payments WHERE user_id=$1 ORDER BY created_at DESC LIMIT 1`, uid).Scan(&ppc)
	rec = do("POST", "/v1/payments/stars/verify", `{"provider_payment_id":"`+ppc+`"}`)
	var vv map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &vv)
	if rec.Code != 200 || vv["status"] != "pending" {
		t.Fatalf("verify: %d %s", rec.Code, rec.Body.String())
	}
}

func TestE2EExpirePending(t *testing.T) {
	pg, _, uid := paySetup(t)
	ctx := context.Background()
	svc := New(pg, me.New(pg, nil))
	_ = svc
	if _, err := pg.Exec(ctx, `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, status, created_at)
		SELECT $1, id, 'month_299', 299, 'tg_stars', 'old:e2e', 299, 'pending', now() - interval '16 minutes'
		  FROM plans WHERE code='month_299' LIMIT 1`, uid); err != nil {
		t.Fatal(err)
	}
	n, err := svc.ExpirePending(ctx)
	if err != nil || n != 1 {
		t.Fatalf("expire: n=%d err=%v", n, err)
	}
}
