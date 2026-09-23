// E2E добивка: entitlements me/plans/bonus, payments verify/adminlist/refund,
// me AgeConfirmed, ai StartWorker, readings generate/streak (см. D-покрытие 70%).
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

func TestE2EMePlansGrant(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, rd)
	au := auth.New(pg, rd)
	r := chi.NewRouter()
	r.With(au.RequireAuth).Get("/v1/entitlements/me", svc.HandleMe)
	r.Get("/v1/plans", svc.HandlePlans)
	uid := testutil.NewUser(t, ctx, pg)
	tok, _ := auth.IssueJWT(uid, auth.UserTTL)
	_ = rd.Set(ctx, "sess:"+uid, "x", auth.UserTTL).Err()
	do := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	var me map[string]any
	if rec := do("/v1/entitlements/me"); rec.Code != 200 {
		t.Fatalf("me: %d", rec.Code)
	} else if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || me["plan"] != "free" {
		t.Fatalf("me: %s", rec.Body.String())
	}
	var plans []map[string]any
	if rec := do("/v1/plans"); rec.Code != 200 {
		t.Fatalf("plans: %d", rec.Code)
	} else if err := json.Unmarshal(rec.Body.Bytes(), &plans); err != nil || len(plans) != 3 {
		t.Fatalf("plans: %s", rec.Body.String())
	}
	// grant bonus → me premium
	if err := svc.GrantBonusDays(ctx, uid, "trial_3d", 3); err != nil {
		t.Fatal(err)
	}
	rec := do("/v1/entitlements/me")
	var me2 map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &me2)
	if me2["plan"] != "premium" {
		t.Fatalf("bonus: %s", rec.Body.String())
	}
	_ = strings.Contains("", "")
}
