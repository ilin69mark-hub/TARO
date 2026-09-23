// E2E referral-antifraud против живых PG/Redis (см. D-покрытие, 02-functional/06).
package referral

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"taro/api/internal/auth"
	"taro/api/internal/entitlements"
	"taro/api/internal/testutil"
)

func testRouter(t *testing.T) (*chi.Mux, func(uid string) string) {
	t.Helper()
	ctx, pg, rd := testutil.Live(t)
	en := entitlements.New(pg, rd)
	svc := New(pg, en)
	au := auth.New(pg, rd)
	r := chi.NewRouter()
	r.With(au.RequireAuth).Get("/v1/referral/me", svc.HandleMe)
	r.With(au.RequireAuth).Post("/v1/referral/apply", svc.HandleApply)
	login := func(uid string) string {
		t.Helper()
		tok, err := auth.IssueJWT(uid, auth.UserTTL)
		if err != nil {
			t.Fatal(err)
		}
		if err := rd.Set(ctx, "sess:"+uid, "x", auth.UserTTL).Err(); err != nil {
			t.Fatal(err)
		}
		return tok
	}
	return r, login
}

func callWith(r *chi.Mux, tok, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestE2EReferralAntifraud(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	r, login := testRouter(t)

	tokA := login(testutil.NewUser(t, ctx, pg))
	tokB := login(testutil.NewUser(t, ctx, pg))

	// A берет код
	rec := callWith(r, tokA, "GET", "/v1/referral/me", "")
	var me map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || me["code"] == nil {
		t.Fatalf("me: %d %s", rec.Code, rec.Body.String())
	}
	code := me["code"].(string)

	// B: self-apply невозможен (свой код) → 409 SELF
	recB := callWith(r, tokB, "GET", "/v1/referral/me", "")
	var meB map[string]any
	_ = json.Unmarshal(recB.Body.Bytes(), &meB)
	recSelf := callWith(r, tokB, "POST", "/v1/referral/apply", `{"code":"`+meB["code"].(string)+`"}`)
	if recSelf.Code != 409 {
		t.Fatalf("self apply: want 409 got %d", recSelf.Code)
	}

	// B применяет код A → pending
	recApply := callWith(r, tokB, "POST", "/v1/referral/apply", `{"code":"`+code+`"}`)
	if recApply.Code != 200 {
		t.Fatalf("apply: want 200 got %d: %s", recApply.Code, recApply.Body.String())
	}
	t.Logf("apply body: %s", recApply.Body.String())

	// повторный apply того же B → 200 (ON CONFLICT DO NOTHING), дубля нет
	recApply2 := callWith(r, tokB, "POST", "/v1/referral/apply", `{"code":"`+code+`"}`)
	if recApply2.Code != 200 {
		t.Fatalf("re-apply: want 200 got %d", recApply2.Code)
	}
	var n int
	_ = pg.QueryRow(ctx,
		`SELECT COUNT(*) FROM referrals`).Scan(&n)
	if n != 1 {
		t.Fatalf("want exactly 1 referral row, got %d", n)
	}
}

func TestE2ECompleteHook(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	en := entitlements.New(pg, rd)
	svc := New(pg, en)

	// referrer с кодом + referee с tg
	var referrer, referee string
	if err := pg.QueryRow(ctx,
		`INSERT INTO users (anon_uuid) VALUES (gen_random_uuid()) RETURNING id`).Scan(&referrer); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, referrer)
	})
	if err := pg.QueryRow(ctx,
		`INSERT INTO users (tg_id) VALUES (810001) RETURNING id`).Scan(&referee); err != nil {
		_, _ = pg.Exec(ctx, `DELETE FROM users WHERE tg_id=810001`)
		if err := pg.QueryRow(ctx,
			`INSERT INTO users (tg_id) VALUES (810001) RETURNING id`).Scan(&referee); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, referee)
	})
	var code string
	if err := pg.QueryRow(ctx,
		`UPDATE users SET referral_code='HOOKTEST' WHERE id=$1 RETURNING referral_code`, referrer).Scan(&code); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx,
		`INSERT INTO referrals (referrer_id, referee_id, code, status, bonus_days) VALUES ($1,$2,'HOOKTEST','pending',3)`,
		referrer, referee); err != nil {
		t.Fatal(err)
	}
	svc.CompleteOnFirstReading(ctx, referee)
	var st string
	if err := pg.QueryRow(ctx,
		`SELECT status FROM referrals WHERE referrer_id=$1 AND referee_id=$2`, referrer, referee).Scan(&st); err != nil || st != "completed" {
		t.Fatalf("hook: status=%s err=%v", st, err)
	}
	var bonus int
	_ = pg.QueryRow(ctx,
		`SELECT COUNT(*) FROM subscriptions WHERE plan_code='referral_bonus' AND (user_id=$1 OR user_id=$2)`,
		referrer, referee).Scan(&bonus)
	if bonus != 2 {
		t.Fatalf("want 2 bonus subs, got %d", bonus)
	}
	// повторный хук — идемпотентен (pending больше нет)
	svc.CompleteOnFirstReading(ctx, referee)
}
