// E2E payments-добивка: verify/adminlist/refund-manual + AgeConfirmed (см. D).
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

func extraSetup(t *testing.T) (*pgxpool.Pool, func(tok, method, path, body string) *httptest.ResponseRecorder, string, string) {
	t.Helper()
	ctx, pg, rd := testutil.Live(t)
	mv := me.New(pg, rd)
	svc := New(pg, mv)
	au := auth.New(pg, rd)
	r := chi.NewRouter()
	r.With(au.RequireAuth).Post("/v1/payments/stars/verify", svc.HandleVerify)
	r.Post("/v1/admin/refund", svc.HandleRefund)
	r.Post("/v1/admin/refund", svc.HandleRefund)
	r.Get("/v1/admin/payments", svc.HandleAdminList)
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
	if !mv.AgeConfirmed(ctx, uid) {
		t.Fatal("age must be true")
	}
	do := func(tok, method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if tok != "" {
			req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	return pg, do, tok, uid
}

func TestE2EVerifyAdminListRefund(t *testing.T) {
	_, do, tok, _ := extraSetup(t)

	// verify несуществующего → 404
	if rec := do(tok, "POST", "/v1/payments/stars/verify", `{"payment_id":"00000000-0000-0000-0000-000000000000"}`); rec.Code != 404 {
		t.Fatalf("verify miss: want 404 got %d", rec.Code)
	}
	// verify без тела → 422
	if rec := do(tok, "POST", "/v1/payments/stars/verify", `{}`); rec.Code != 422 {
		t.Fatalf("verify empty: want 422 got %d", rec.Code)
	}
	// admin list → 200 массив
	rec := do("", "GET", "/v1/admin/payments?limit=3", "")
	var list []map[string]any
	if rec.Code != 200 {
		t.Fatalf("adminlist: %d", rec.Code)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
}

func TestE2ERefundPaths(t *testing.T) {
	pg, do, _, uid := extraSetup(t)
	ctx := context.Background()

	mkpay := func() string {
		var id string
		if err := pg.QueryRow(ctx, `
			INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status)
			SELECT $1, id, 'month_299', 299, 'tg_stars', 'test:refund-' || gen_random_uuid(), 299, 199, 'succeeded'
			  FROM plans WHERE code='month_299' LIMIT 1 RETURNING id`, uid).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	// 1. без tg-данных → ручной путь ok + note
	pid := mkpay()
	if rec := do("", "POST", "/v1/admin/refund", `{"payment_id":"`+pid+`"}`); rec.Code != 200 {
		t.Fatalf("manual refund: %d %s", rec.Code, rec.Body.String())
	}
	var note *string
	var st string
	_ = pg.QueryRow(ctx, `SELECT status, note FROM payments WHERE id=$1`, pid).Scan(&st, &note)
	if st != "refunded" || note == nil {
		t.Fatalf("manual: status=%s note=%v", st, note)
	}

	// 2. с tg_id, dev-токен → TG падает → 502 без порчи
	if _, err := pg.Exec(ctx, `UPDATE users SET tg_id=820001 WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	pid2 := mkpay()
	_, _ = pg.Exec(ctx, `UPDATE payments SET provider_payment_id='tg:ch_x' WHERE id=$1`, pid2)
	if rec := do("", "POST", "/v1/admin/refund", `{"payment_id":"`+pid2+`"}`); rec.Code != 502 {
		t.Fatalf("tg-fail refund: want 502 got %d %s", rec.Code, rec.Body.String())
	}
	var st2 string
	_ = pg.QueryRow(ctx, `SELECT status FROM payments WHERE id=$1`, pid2).Scan(&st2)
	if st2 != "succeeded" {
		t.Fatalf("failed refund must not touch: %s", st2)
	}
	_, _ = pg.Exec(ctx, `UPDATE users SET tg_id=NULL WHERE id=$1`, uid)

	// 3. несуществующий → 404; пустое тело → 422
	if rec := do("", "POST", "/v1/admin/refund", `{"payment_id":"00000000-0000-0000-0000-000000000000"}`); rec.Code != 404 {
		t.Fatalf("refund miss: %d", rec.Code)
	}
	if rec := do("", "POST", "/v1/admin/refund", `{}`); rec.Code != 422 {
		t.Fatalf("refund empty: %d", rec.Code)
	}
}

func TestE2EYooKassaFlag(t *testing.T) {
	pg, do, _, _ := extraSetup(t)
	ctx := context.Background()
	// enabled=true → провайдер узнан, но без KYC все равно 501? Нет: enabled флаг
	// означает пройденный KYC — без реализации возвращаем 501 только когда выкл.
	// Здесь проверяем выкл-путь (дефолт): invoice уже покрыт; флаг напрямую:
	yk := YooKassaProvider{pg: pg}
	if _, err := yk.CreateInvoice(ctx, nil, "p", "month_299", 0, 299); err == nil {
		t.Fatal("want disabled error")
	}
	if _, err := pg.Exec(ctx, `INSERT INTO app_config (key, value)
		VALUES ('payments.yookassa','{"enabled":true}') ON CONFLICT (key) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	defer pg.Exec(context.Background(), `DELETE FROM app_config WHERE key='payments.yookassa'`)
	_ = do
}
