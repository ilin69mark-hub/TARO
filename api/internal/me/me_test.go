// E2E DELETE /v1/me + age против живых PG/Redis (compose up db cache).
// Запуск: DATABASE_URL=... REDIS_ADDR=... go test ./internal/me/ -run E2E -v
package me

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"taro/api/internal/auth"
	"taro/api/internal/store"
)

func TestE2EDeleteMe(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("no DATABASE_URL")
	}
	ctx := context.Background()
	pg, err := store.ConnectPG(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	rd := store.ConnectRedis()
	defer rd.Close()

	au := auth.New(pg, rd)
	mv := New(pg, rd)

	// создаем юзера напрямую
	var uid string
	if err := pg.QueryRow(ctx,
		`INSERT INTO users (anon_uuid) VALUES (gen_random_uuid()) RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	tok, err := auth.IssueJWT(uid, auth.UserTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err := rd.Set(ctx, "sess:"+uid, "x", auth.UserTTL).Err(); err != nil {
		t.Fatal(err)
	}
	call := func(method, target, body, cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF", "test")
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: cookie})
		}
		rec := httptest.NewRecorder()
		mux := http.NewServeMux()
		mux.Handle("/v1/me/age", au.RequireAuth(http.HandlerFunc(mv.HandleAge)))
		mux.Handle("/v1/me", au.RequireAuth(http.HandlerFunc(mv.HandleDelete)))
		mux.ServeHTTP(rec, req)
		return rec
	}

	// age без confirmed → 422
	if rec := call("POST", "/v1/me/age", `{"confirmed":false}`, tok); rec.Code != 422 {
		t.Fatalf("age false: want 422 got %d", rec.Code)
	}
	// age ok
	if rec := call("POST", "/v1/me/age", `{"confirmed":true}`, tok); rec.Code != 200 {
		t.Fatalf("age true: want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	var confirmed bool
	_ = pg.QueryRow(ctx, `SELECT age_confirmed_at IS NOT NULL FROM users WHERE id=$1`, uid).Scan(&confirmed)
	if !confirmed {
		t.Fatal("age not stored")
	}
	// чтение юзера для проверки каскада
	if _, err := pg.Exec(ctx,
		`INSERT INTO readings (user_id, spread_code, question, cards, seed, status) VALUES ($1,'daily','q','[]',1,'done')`, uid); err != nil {
		t.Fatal(err)
	}
	// delete без confirm → 422
	if rec := call("DELETE", "/v1/me", ``, tok); rec.Code != 422 {
		t.Fatalf("delete noconfirm: want 422 got %d", rec.Code)
	}
	// delete
	if rec := call("DELETE", "/v1/me", `{"confirm":"DELETE"}`, tok); rec.Code != 200 {
		t.Fatalf("delete: want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	var users, readings int
	_ = pg.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE id=$1`, uid).Scan(&users)
	_ = pg.QueryRow(ctx, `SELECT COUNT(*) FROM readings WHERE user_id=$1`, uid).Scan(&readings)
	if users != 0 || readings != 0 {
		t.Fatalf("not wiped: users=%d readings=%d", users, readings)
	}
	if n, _ := rd.Exists(ctx, "sess:"+uid).Result(); n != 0 {
		t.Fatal("sess not deleted")
	}
	// токен мертв: RequireAuth → 401
	req := httptest.NewRequest("DELETE", "/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
	rec := httptest.NewRecorder()
	au.RequireAuth(http.HandlerFunc(mv.HandleDelete)).ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("after delete want 401 got %d", rec.Code)
	}
}
