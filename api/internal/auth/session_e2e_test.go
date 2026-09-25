// E2E auth-сессии: RequireAuth, refresh, logout, CSRF per-session (см. D, S07).
package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"

	"taro/api/internal/testutil"
)

func sessionRouter(t *testing.T) (*chi.Mux, *Service, *redis.Client, func() (string, string, string)) {
	t.Helper()
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, rd)
	r := chi.NewRouter()
	r.Use(svc.RequireCSRF)
	r.Post("/v1/auth/anon", svc.HandleAnon)
	r.With(svc.RequireAuth).Post("/v1/auth/refresh", svc.HandleRefresh)
	r.With(svc.RequireAuth).Post("/v1/auth/logout", svc.HandleLogout)
	login := func() (string, string, string) {
		t.Helper()
		uid := testutil.NewUser(t, ctx, pg)
		tok, err := IssueJWT(uid, UserTTL)
		if err != nil {
			t.Fatal(err)
		}
		if err := rd.Set(ctx, sessKey(uid), "x", UserTTL).Err(); err != nil {
			t.Fatal(err)
		}
		csrf, err := svc.csrfFor(ctx, uid)
		if err != nil {
			t.Fatal(err)
		}
		return tok, uid, csrf
	}
	_ = context.Background
	return r, svc, rd, login
}

func TestE2EAuthGuards(t *testing.T) {
	r, _, _, login := sessionRouter(t)
	tok, _, csrf := login()

	post := func(tok, path, body, csrfHdr string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if csrfHdr != "" {
			req.Header.Set("X-CSRF", csrfHdr)
		}
		if tok != "" {
			req.AddCookie(&http.Cookie{Name: CookieName, Value: tok})
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	postOrigin := func(tok, path, body, csrfHdr, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if csrfHdr != "" {
			req.Header.Set("X-CSRF", csrfHdr)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if tok != "" {
			req.AddCookie(&http.Cookie{Name: CookieName, Value: tok})
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// чужой Origin на входе без сессии → 403 (S07)
	if rec := postOrigin("", "/v1/auth/anon", `{"uuid":"x"}`, "", "https://evil.example"); rec.Code != 403 {
		t.Fatalf("evil origin: want 403 got %d", rec.Code)
	}
	// чужой CSRF → 403 (S07: константа "1" больше не проходит)
	if rec := post(tok, "/v1/auth/refresh", `{}`, "1"); rec.Code != 403 {
		t.Fatalf("fake csrf: want 403 got %d", rec.Code)
	}
	// без cookie → 401
	if rec := post("", "/v1/auth/refresh", `{}`, csrf); rec.Code != 401 {
		t.Fatalf("no cookie: want 401 got %d", rec.Code)
	}
	// refresh ok с настоящим токеном
	if rec := post(tok, "/v1/auth/refresh", `{}`, csrf); rec.Code != 200 {
		t.Fatalf("refresh: want 200 got %d: %s", rec.Code, rec.Body.String())
	} else {
		for _, c := range rec.Result().Cookies() {
			if c.Name == CookieName {
				tok = c.Value
			}
		}
	}
	// logout ok
	if rec := post(tok, "/v1/auth/logout", `{}`, csrf); rec.Code != 200 {
		t.Fatalf("logout: want 200 got %d", rec.Code)
	}
	// после logout токен мертв
	if rec := post(tok, "/v1/auth/refresh", `{}`, csrf); rec.Code != 401 {
		t.Fatalf("after logout: want 401 got %d", rec.Code)
	}
}

func TestE2ERefreshRejectedCSRFDoesNotRotateSession(t *testing.T) {
	r, _, rd, login := sessionRouter(t)
	tok, uid, csrf := login()
	before, err := rd.Get(context.Background(), sessKey(uid)).Result()
	if err != nil {
		t.Fatal(err)
	}
	post := func(value string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/auth/refresh", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF", value)
		req.AddCookie(&http.Cookie{Name: CookieName, Value: tok})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if rec := post("invalid"); rec.Code != http.StatusForbidden {
		t.Fatalf("invalid CSRF: want 403 got %d: %s", rec.Code, rec.Body.String())
	}
	afterRejected, err := rd.Get(context.Background(), sessKey(uid)).Result()
	if err != nil {
		t.Fatal(err)
	}
	if afterRejected != before {
		t.Fatal("rejected CSRF rotated the session")
	}
	if rec := post(csrf); rec.Code != http.StatusOK {
		t.Fatalf("valid refresh: %d %s", rec.Code, rec.Body.String())
	}
	afterAccepted, err := rd.Get(context.Background(), sessKey(uid)).Result()
	if err != nil {
		t.Fatal(err)
	}
	if afterAccepted == before {
		t.Fatal("valid refresh did not rotate the session")
	}
}

func TestE2ECSRFRedisLossSetsReplacementCookie(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-0123456789abcdef0123456789")
	r, _, rd, login := sessionRouter(t)
	tok, uid, oldCSRF := login()
	if err := rd.Del(context.Background(), "csrf:"+uid).Err(); err != nil {
		t.Fatal(err)
	}
	post := func(value string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/auth/refresh", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF", value)
		req.AddCookie(&http.Cookie{Name: CookieName, Value: tok})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	rec := post(oldCSRF)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("stale CSRF: want 403 got %d: %s", rec.Code, rec.Body.String())
	}
	var replacement *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "taro_csrf" {
			replacement = cookie
		}
	}
	if replacement == nil || replacement.Value == "" || replacement.Value == oldCSRF {
		t.Fatalf("replacement CSRF cookie missing: %#v", replacement)
	}
	if retry := post(replacement.Value); retry.Code != http.StatusOK {
		t.Fatalf("replacement CSRF rejected: %d %s", retry.Code, retry.Body.String())
	}
}

func TestE2EAuthLogoutRedisFailure(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-0123456789abcdef0123456789")
	_, _, _, login := sessionRouter(t)
	_, uid, _ := login()
	broken := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  10 * time.Millisecond,
		ReadTimeout:  10 * time.Millisecond,
		WriteTimeout: 10 * time.Millisecond,
		MaxRetries:   -1,
	})
	t.Cleanup(func() { _ = broken.Close() })
	svc := New(nil, broken)
	req := httptest.NewRequest("POST", "/v1/auth/logout", strings.NewReader(`{}`))
	req = req.WithContext(context.WithValue(req.Context(), userCtxKey{}, uid))
	rec := httptest.NewRecorder()
	svc.HandleLogout(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("logout Redis failure: %d %s", rec.Code, rec.Body.String())
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("logout cleared cookies after failed revocation")
	}
}

func TestE2ECSRedisFailureDoesNotExpireCookies(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-0123456789abcdef0123456789")
	_, _, _, login := sessionRouter(t)
	tok, _, csrf := login()
	svc := New(nil, nil)
	req := httptest.NewRequest("POST", "/v1/auth/refresh", strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: CookieName, Value: tok})
	req.Header.Set("X-CSRF", csrf)
	rec := httptest.NewRecorder()
	svc.RequireCSRF(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler called after Redis failure")
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("Redis failure: want 503 got %d: %s", rec.Code, rec.Body.String())
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("Redis failure expired auth cookies")
	}
}

func TestE2EUUIDHardening(t *testing.T) {
	r, _, _, _ := sessionRouter(t)
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/auth/anon", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	// мусор вместо UUID → 422 до БД (S08)
	for _, bad := range []string{`{"uuid":"x"}`, `{"uuid":"bbbbbbbb-1234-0000-000000000000"}`, `{"uuid":""}`, `{}`} {
		if rec := post(bad); rec.Code != 422 {
			t.Fatalf("bad uuid %s: want 422 got %d", bad, rec.Code)
		}
	}
	// чужой fingerprint на знакомом uuid → 403 (S08)
	u := "aaaaaaaa-1111-2222-3333-444444444444"
	if rec := post(`{"uuid":"` + u + `","fingerprint":"fp-A"}`); rec.Code != 200 {
		t.Fatalf("first fp: %d", rec.Code)
	}
	if rec := post(`{"uuid":"` + u + `","fingerprint":"fp-B"}`); rec.Code != 403 {
		t.Fatalf("fp mismatch: want 403 got %d", rec.Code)
	}
	// тот же fingerprint → 200
	if rec := post(`{"uuid":"` + u + `","fingerprint":"fp-A"}`); rec.Code != 200 {
		t.Fatalf("same fp: %d", rec.Code)
	}
}
