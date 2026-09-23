// E2E auth-сессии: RequireAuth, refresh, logout, CSRF (см. D-покрытие, T08–T10).
package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"taro/api/internal/testutil"
)

func sessionRouter(t *testing.T) (*chi.Mux, *Service, func() (string, string)) {
	t.Helper()
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, rd)
	r := chi.NewRouter()
	r.Use(RequireCSRF)
	r.Post("/v1/auth/anon", svc.HandleAnon)
	r.With(svc.RequireAuth).Post("/v1/auth/refresh", svc.HandleRefresh)
	r.With(svc.RequireAuth).Post("/v1/auth/logout", svc.HandleLogout)
	login := func() (string, string) {
		t.Helper()
		uid := testutil.NewUser(t, ctx, pg)
		tok, err := IssueJWT(uid, UserTTL)
		if err != nil {
			t.Fatal(err)
		}
		if err := rd.Set(ctx, sessKey(uid), "x", UserTTL).Err(); err != nil {
			t.Fatal(err)
		}
		return tok, uid
	}
	return r, svc, login
}

func TestE2EAuthGuards(t *testing.T) {
	r, _, login := sessionRouter(t)
	tok, _ := login()

	post := func(tok, path, body string, csrf bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if csrf {
			req.Header.Set("X-CSRF", "1")
		}
		if tok != "" {
			req.AddCookie(&http.Cookie{Name: CookieName, Value: tok})
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// без CSRF → 403 даже с валидным телом
	if rec := post("", "/v1/auth/anon", `{"uuid":"x"}`, false); rec.Code != 403 {
		t.Fatalf("no csrf: want 403 got %d", rec.Code)
	}
	// без cookie → 401
	if rec := post("", "/v1/auth/refresh", `{}`, true); rec.Code != 401 {
		t.Fatalf("no cookie: want 401 got %d", rec.Code)
	}
	// refresh ok
	if rec := post(tok, "/v1/auth/refresh", `{}`, true); rec.Code != 200 {
		t.Fatalf("refresh: want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	// logout ok
	if rec := post(tok, "/v1/auth/logout", `{}`, true); rec.Code != 200 {
		t.Fatalf("logout: want 200 got %d", rec.Code)
	}
	// после logout токен мертв
	if rec := post(tok, "/v1/auth/refresh", `{}`, true); rec.Code != 401 {
		t.Fatalf("after logout: want 401 got %d", rec.Code)
	}
}
