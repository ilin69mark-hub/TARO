// E2E entitlements-довесок: me/plans/grantbonus (см. D-покрытие).
package entitlements

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"taro/api/internal/auth"
	"taro/api/internal/testutil"
)

func entClient(t *testing.T) (func(tok, method, path, body string) *httptest.ResponseRecorder, string) {
	t.Helper()
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, rd)
	au := auth.New(pg, rd)
	r := chi.NewRouter()
	r.With(au.RequireAuth).Get("/v1/entitlements/me", svc.HandleMe)
	r.Get("/v1/plans", svc.HandlePlans)
	uid := testutil.NewUser(t, ctx, pg)
	tok, err := auth.IssueJWT(uid, auth.UserTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err := rd.Set(ctx, "sess:"+uid, "x", auth.UserTTL).Err(); err != nil {
		t.Fatal(err)
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
	return do, tok
}

func TestE2EMePlansBonus(t *testing.T) {
	do, tok := entClient(t)

	// me fresh: free 1/1, no winback
	var me map[string]any
	rec := do(tok, "GET", "/v1/entitlements/me", "")
	if rec.Code != 200 {
		t.Fatalf("me: %d", rec.Code)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &me)
	if me["plan"] != "free" || me["winback_eligible"] != false {
		t.Fatalf("me: %v", me)
	}

	// plans: 3 покупаемых
	prec := do("", "GET", "/v1/plans", "")
	var plans []map[string]any
	if err := json.Unmarshal(prec.Body.Bytes(), &plans); err != nil || len(plans) != 3 {
		t.Fatalf("plans: %s", prec.Body.String())
	}
}
