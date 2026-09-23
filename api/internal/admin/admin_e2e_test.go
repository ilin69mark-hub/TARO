// E2E admin: RequireAdmin, config full, publish apply/audit, rotate (см. D1).
package admin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"taro/api/internal/auth"
	"taro/api/internal/testutil"
)

// craftInit строит валидный initData под заданный botToken (см. auth/craft).
func craftInit(t *testing.T, tgID int64) string {
	t.Helper()
	bot := ""
	q := url.Values{}
	q.Set("user", `{"id":`+strconv.FormatInt(tgID, 10)+`}`)
	q.Set("auth_date", strconv.FormatInt(time.Now().Unix(), 10))
	pairs := []string{}
	for k, vv := range q {
		pairs = append(pairs, k+"="+strings.Join(vv, ","))
	}
	sort.Strings(pairs)
	mk := hmac.New(sha256.New, []byte("WebAppData"))
	mk.Write([]byte(bot))
	mac := hmac.New(sha256.New, mk.Sum(nil))
	mac.Write([]byte(strings.Join(pairs, "\n")))
	q.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return q.Encode()
}

func quote(s string) string { return strconv.Quote(s) }

func adminSetup(t *testing.T) (*chi.Mux, string, *pgxpool.Pool) {
	t.Helper()
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, rd)
	au := auth.New(pg, rd)
	_ = au
	r := chi.NewRouter()
	r.Post("/v1/admin/login", svc.HandleLogin)
	r.With(svc.RequireAdmin).Get("/v1/admin/config", svc.HandleGetConfig)
	r.With(svc.RequireAdmin).Post("/v1/admin/config/publish", svc.HandlePublish)
	r.With(svc.RequireAdmin).Post("/v1/admin/rotate-seasonal", svc.HandleRotateSeasonal)

	var uid string
	if err := pg.QueryRow(ctx,
		`INSERT INTO users (tg_id, role) VALUES (987654321,'admin') RETURNING id`).Scan(&uid); err != nil {
		_ = pg.QueryRow(ctx, `SELECT id FROM users WHERE tg_id=987654321`).Scan(&uid)
		_, _ = pg.Exec(ctx, `UPDATE users SET role='admin' WHERE id=$1`, uid)
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE tg_id=987654321`)
	})
	tok, err := auth.IssueJWT("admin:"+uid, AdminTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err := rd.Set(ctx, "sess:admin:"+uid, "1", AdminTTL).Err(); err != nil {
		t.Fatal(err)
	}
	return r, tok, pg
}

func acall(r *chi.Mux, tok, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.AddCookie(&http.Cookie{Name: AdminCookie, Value: tok})
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestE2EAdminAuth(t *testing.T) {
	r, tok, _ := adminSetup(t)
	if rec := acall(r, "", "GET", "/v1/admin/config", ""); rec.Code != 403 {
		t.Fatalf("no cookie: want 403 got %d", rec.Code)
	}
	if rec := acall(r, "junk", "GET", "/v1/admin/config", ""); rec.Code != 403 {
		t.Fatalf("junk token: want 403 got %d", rec.Code)
	}
	rec := acall(r, tok, "GET", "/v1/admin/config", "")
	if rec.Code != 200 {
		t.Fatalf("config: want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	var cfg map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"app_config", "plans", "spreads"} {
		if cfg[k] == nil {
			t.Fatalf("config missing %s", k)
		}
	}
}

func TestE2EAdminLogin(t *testing.T) {
	r, _, _ := adminSetup(t)
	// чужой initDataCraft невозможен без токена — проверяем отказ без подписи
	if rec := acall(r, "", "POST", "/v1/admin/login", `{"initData":"user=%7B%22id%22%3A1%7D&hash=x"}`); rec.Code != 401 {
		t.Fatalf("bad login: want 401 got %d", rec.Code)
	}
	// позитивный: craft с пустым токеном (env пуст) + whitelist
	t.Setenv("TG_BOT_TOKEN", "")
	t.Setenv("ADMIN_TG_IDS", "777001")
	init := craftInit(t, 777001)
	rec := acall(r, "", "POST", "/v1/admin/login", `{"initData":`+quote(init)+`}`)
	if rec.Code != 200 {
		t.Fatalf("login: want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == AdminCookie && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("no taro_admin cookie")
	}
	// тот же tg снова — уже role=admin, тоже ок
	rec = acall(r, "", "POST", "/v1/admin/login", `{"initData":`+quote(init)+`}`)
	if rec.Code != 200 {
		t.Fatalf("relogin: %d", rec.Code)
	}
	// чужой tg без whitelist → 403
	init2 := craftInit(t, 777002)
	if rec := acall(r, "", "POST", "/v1/admin/login", `{"initData":`+quote(init2)+`}`); rec.Code != 403 {
		t.Fatalf("stranger: want 403 got %d", rec.Code)
	}
}

func TestE2EPublishAudit(t *testing.T) {
	r, tok, pg := adminSetup(t)
	// плохой ключ → 422, ничего не применено
	if rec := acall(r, tok, "POST", "/v1/admin/config/publish", `{"app_config":{"nope":1}}`); rec.Code != 422 {
		t.Fatalf("bad key: want 422 got %d", rec.Code)
	}
	// валидный diff: текст + сортировка spreads
	rec := acall(r, tok, "POST", "/v1/admin/config/publish",
		`{"app_config":{"copy.paywall_cta":"E2E"},"spreads":[{"code":"daily","sort_order":11}]}`)
	if rec.Code != 200 {
		t.Fatalf("publish: want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["ok"] != true {
		t.Fatalf("publish not ok: %s", rec.Body.String())
	}
	// audit-запись есть
	var audits int
	_ = pg.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM admin_audit WHERE action='config:publish'`).Scan(&audits)
	if audits < 1 {
		t.Fatal("no audit row")
	}
	// откат тестовых изменений
	_, _ = pg.Exec(context.Background(), `UPDATE spreads SET sort_order=10 WHERE code='daily'`)
	_, _ = pg.Exec(context.Background(),
		`UPDATE app_config SET value='"Продолжить безлимитно — 299₽/мес"' WHERE key='copy.paywall_cta'`)
	_, _ = pg.Exec(context.Background(), `DELETE FROM admin_audit WHERE action='config:publish' AND diff::text LIKE '%E2E%'`)
}

func TestE2ERotateSeasonal(t *testing.T) {
	r, tok, pg := adminSetup(t)
	ctx := context.Background()
	if _, err := pg.Exec(ctx, `INSERT INTO app_config (key, value)
		VALUES ('spreads.seasonal','[{"code":"fullmoon","from":"2000-01-01","to":"2100-12-31"}]')
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value`); err != nil {
		t.Fatal(err)
	}
	defer pg.Exec(context.Background(), `DELETE FROM app_config WHERE key='spreads.seasonal'`)
	defer pg.Exec(context.Background(), `UPDATE spreads SET is_active=false WHERE code='fullmoon'`)
	rec := acall(r, tok, "POST", "/v1/admin/rotate-seasonal", `{}`)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != 200 || out["ok"] != true {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body.String())
	}
	var active bool
	_ = pg.QueryRow(ctx, `SELECT is_active FROM spreads WHERE code='fullmoon'`).Scan(&active)
	if !active {
		t.Fatal("fullmoon not activated")
	}
}
