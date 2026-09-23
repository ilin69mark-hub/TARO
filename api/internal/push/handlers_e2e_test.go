// E2E push-хендлеры: prefs/evening/streak/stats/remind (см. D-покрытие).
package push

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

func pushClient(t *testing.T) (func(tok, method, path, body string) *httptest.ResponseRecorder, string) {
	t.Helper()
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg)
	au := auth.New(pg, rd)
	r := chi.NewRouter()
	r.Get("/v1/push/public", svc.HandlePublicKey)
	r.With(au.RequireAuth).Post("/v1/push/subscribe", svc.HandleSubscribe)
	r.With(au.RequireAuth).Delete("/v1/push/unsubscribe", svc.HandleUnsubscribe)
	r.With(au.RequireAuth).Get("/v1/push/prefs", svc.HandleGetPrefs)
	r.With(au.RequireAuth).Post("/v1/push/prefs", svc.HandleSetPrefs)
	r.Post("/v1/admin/push-evening", svc.HandleEvening)
	r.Post("/v1/admin/push-streak-risk", svc.HandleStreakRisk)
	r.Get("/v1/admin/push-stats", svc.HandlePushStats)
	r.Post("/v1/admin/remind-expiring", svc.HandleRemindExpiring)
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

func TestE2EPushHandlers(t *testing.T) {
	do, tok := pushClient(t)
	t.Setenv("VAPID_PUBLIC_KEY", "BKApO8VWk4H1WeiqB1IHuJPVHPendNUt7KXbVIdGuhdOq3s2m3du4PMw-WtfuqaQnJIlFML1IBWjKKnNpgpjX6M")

	// public без auth
	if rec := do("", "GET", "/v1/push/public", ""); rec.Code != 200 {
		t.Fatalf("public: %d", rec.Code)
	}
	// prefs без токена → 401
	if rec := do("", "GET", "/v1/push/prefs", ""); rec.Code != 401 {
		t.Fatalf("prefs anon: want 401 got %d", rec.Code)
	}
	// prefs дефолт
	var p map[string]any
	rec := do(tok, "GET", "/v1/push/prefs", "")
	if rec.Code != 200 {
		t.Fatalf("prefs: %d", rec.Code)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p["hour"] != float64(21) || p["quiet"] != false {
		t.Fatalf("prefs default: %v", p)
	}
	// bad hour → 422
	if rec := do(tok, "POST", "/v1/push/prefs", `{"hour":99}`); rec.Code != 422 {
		t.Fatalf("hour: want 422 got %d", rec.Code)
	}
	// set ok
	if rec := do(tok, "POST", "/v1/push/prefs", `{"hour":20,"quiet":true}`); rec.Code != 200 {
		t.Fatalf("set: %d", rec.Code)
	}
	// subscribe bad → 422; SSRF (169.254, http, 127.0.0.1) → 422 (см. S02)
	for _, bad := range []string{
		`{"endpoint":"http://169.254.169.254/x","p256dh":"AA","auth":"BB"}`,
		`{"endpoint":"http://example.com/x","p256dh":"AA","auth":"BB"}`,
		`{"endpoint":"https://127.0.0.1:8081/x","p256dh":"AA","auth":"BB"}`,
		`{"endpoint":"https://user:pass@push.example/x","p256dh":"AA","auth":"BB"}`,
	} {
		if rec := do(tok, "POST", "/v1/push/subscribe", bad); rec.Code != 422 {
			t.Fatalf("ssrf %s: want 422 got %d", bad[:40], rec.Code)
		}
	}
	sub := `{"endpoint":"https://example.com/e2e1","p256dh":"AA","auth":"BB"}`
	if rec := do(tok, "POST", "/v1/push/subscribe", sub); rec.Code != 200 {
		t.Fatalf("sub: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(tok, "DELETE", "/v1/push/unsubscribe", `{"endpoint":"https://example.com/e2e1"}`); rec.Code != 200 {
		t.Fatalf("unsub: %d", rec.Code)
	}
	// вечерняя/стрик/реминдер/статы без подписок — ok с нулями
	for _, path := range []string{"/v1/admin/push-evening", "/v1/admin/push-streak-risk", "/v1/admin/remind-expiring"} {
		if rec := do("", "POST", path, `{}`); rec.Code != 200 {
			t.Fatalf("%s: want 200 got %d: %s", path, rec.Code, rec.Body.String())
		}
	}
	if rec := do("", "GET", "/v1/admin/push-stats", ""); rec.Code != 200 {
		t.Fatalf("stats: %d", rec.Code)
	}
}
