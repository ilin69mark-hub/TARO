// E2E diary update/delete против живых PG/Redis.
// Запуск: DATABASE_URL=... REDIS_ADDR=... JWT_SECRET=... go test ./internal/diary/ -run E2E -v
package diary

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"taro/api/internal/auth"
	"taro/api/internal/store"

	"github.com/go-chi/chi/v5"
)

func TestE2EDiaryUD(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("no DATABASE_URL")
	}
	ctx := context.Background()
	pg, err := store.ConnectPG(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()

	au := auth.New(pg, store.ConnectRedis())
	dv := New(pg)
	var uid string
	if err := pg.QueryRow(ctx,
		`INSERT INTO users (anon_uuid) VALUES (gen_random_uuid()) RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	defer pg.Exec(ctx, `DELETE FROM users WHERE id=$1`, uid)
	tok, _ := auth.IssueJWT(uid, auth.UserTTL)

	// проще: напрямую через RequireAuth + chi-маршруты с sess в redis
	rd := store.ConnectRedis()
	defer rd.Close()
	if err := rd.Set(ctx, "sess:"+uid, "x", auth.UserTTL).Err(); err != nil {
		t.Fatal(err)
	}
	do := func(method, target, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF", "test")
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
		rec := httptest.NewRecorder()
		r := chi.NewRouter()
		r.Use(au.RequireAuth)
		r.Put("/v1/diary/{id}", dv.HandleUpdate)
		r.Delete("/v1/diary/{id}", dv.HandleDelete)
		r.ServeHTTP(rec, req)
		return rec
	}

	var eid string
	if err := pg.QueryRow(ctx,
		`INSERT INTO diary_entries (user_id, body, mood) VALUES ($1,'черновик','down') RETURNING id`, uid).Scan(&eid); err != nil {
		t.Fatal(err)
	}
	if rec := do("PUT", "/v1/diary/"+eid, `{"body":"уже лучше","mood":"calm"}`); rec.Code != 200 {
		t.Fatalf("put: want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := do("DELETE", "/v1/diary/"+eid, ``); rec.Code != 200 {
		t.Fatalf("delete: want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	var n int
	_ = pg.QueryRow(ctx, `SELECT COUNT(*) FROM diary_entries WHERE id=$1`, eid).Scan(&n)
	if n != 0 {
		t.Fatal("not deleted")
	}
}
