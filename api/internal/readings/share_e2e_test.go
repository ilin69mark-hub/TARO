package readings

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"taro/api/internal/ai"
	"taro/api/internal/auth"
	"taro/api/internal/entitlements"
	"taro/api/internal/referral"
	"taro/api/internal/testutil"
)

// E2E токен-шеринга: создание владельцем, публичное превью без толкования, чужой id → 404.
func TestE2EShareToken(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	en := entitlements.New(pg, rd)
	gw := ai.New(pg, rd)
	rf := referral.New(pg, en)
	svc := New(pg, en, gw, rf)
	au := auth.New(pg, rd)
	uid := testutil.NewUser(t, ctx, pg)
	tok, err := auth.IssueJWT(uid, auth.UserTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err := rd.Set(ctx, "sess:"+uid, "x", auth.UserTTL).Err(); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.With(au.RequireAuth).Post("/v1/readings", svc.HandleCreate)
	r.With(au.RequireAuth).Post("/v1/share", svc.HandleCreateShare)
	r.With(au.RequireAuth).Post("/v1/share/revoke", svc.HandleRevokeShare)
	r.Get("/v1/share/{token}", svc.HandleGetShare)
	do := func(tok, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if tok != "" {
			req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// чтение
	rec := do(tok, "POST", "/v1/readings", `{"spread_code":"daily"}`,
		map[string]string{"Idempotency-Key": "share-e2e-1"})
	if rec.Code != 200 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	id := created["reading_id"]

	// токен
	rec = do(tok, "POST", "/v1/share", `{"reading_id":"`+id+`"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("share create: %d %s", rec.Code, rec.Body.String())
	}
	var tk map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &tk)
	if len(tk["token"]) != 32 {
		t.Fatalf("bad token: %s", rec.Body.String())
	}
	// повтор — тот же токен (стабильность)
	rec = do(tok, "POST", "/v1/share", `{"reading_id":"`+id+`"}`, nil)
	var tk2 map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &tk2)
	if tk2["token"] != tk["token"] {
		t.Fatalf("token unstable: %s vs %s", tk["token"], tk2["token"])
	}

	// публичное превью без auth: вопрос есть, толкования нет
	rec = do("", "GET", "/v1/share/"+tk["token"], "", nil)
	if rec.Code != 200 {
		t.Fatalf("share get: %d %s", rec.Code, rec.Body.String())
	}
	var sh map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &sh)
	if _, hasInterp := sh["interpretation"]; hasInterp {
		t.Fatalf("interpretation leaked in share: %s", rec.Body.String())
	}
	if sh["spread"] == nil {
		t.Fatalf("no spread in share: %s", rec.Body.String())
	}

	// мусорный токен → 422, неизвестный → 404
	if rec := do("", "GET", "/v1/share/xyz", "", nil); rec.Code != 422 {
		t.Fatalf("bad token: want 422 got %d", rec.Code)
	}
	if rec := do("", "GET", "/v1/share/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "", nil); rec.Code != 404 {
		t.Fatalf("unknown token: want 404 got %d", rec.Code)
	}

	// чужой reading_id → 404 (второй юзер)
	uid2 := testutil.NewUser(t, ctx, pg)
	tok2, _ := auth.IssueJWT(uid2, auth.UserTTL)
	_ = rd.Set(ctx, "sess:"+uid2, "x", auth.UserTTL).Err()
	if rec := do(tok2, "POST", "/v1/share", `{"reading_id":"`+id+`"}`, nil); rec.Code != 404 {
		t.Fatalf("foreign share: want 404 got %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(tok, "POST", "/v1/share/revoke", `{"reading_id":"`+id+`"}`, nil); rec.Code != 200 {
		t.Fatalf("revoke: want 200 got %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("", "GET", "/v1/share/"+tk["token"], "", nil); rec.Code != 404 {
		t.Fatalf("revoked share: want 404 got %d", rec.Code)
	}
	rec = do(tok, "POST", "/v1/share", `{"reading_id":"`+id+`"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("recreate share: %d %s", rec.Code, rec.Body.String())
	}
	var tk3 map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &tk3)
	if tk3["token"] == "" || tk3["token"] == tk["token"] {
		t.Fatalf("token was not rotated after revoke")
	}
	if _, err := pg.Exec(ctx, `UPDATE share_tokens SET expires_at=now()-interval '1 second' WHERE reading_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if rec := do("", "GET", "/v1/share/"+tk3["token"], "", nil); rec.Code != 404 {
		t.Fatalf("expired share: want 404 got %d", rec.Code)
	}
}
