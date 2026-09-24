// E2E diary-хендлеры: create/list/get/export (см. D-покрытие).
package diary

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

func diaryClient(t *testing.T) (func(tok, method, path, body string) *httptest.ResponseRecorder, string) {
	t.Helper()
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg)
	au := auth.New(pg, rd)
	r := chi.NewRouter()
	r.With(au.RequireAuth).Post("/v1/diary", svc.HandleCreate)
	r.With(au.RequireAuth).Get("/v1/diary", svc.HandleList)
	r.With(au.RequireAuth).Post("/v1/diary/export", svc.HandleExport)
	r.With(au.RequireAuth).Get("/v1/diary/{id}", svc.HandleGet)
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
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	return do, tok
}

func TestE2EDiaryHandlers(t *testing.T) {
	do, tok := diaryClient(t)

	// пустое тело → 422
	if rec := do(tok, "POST", "/v1/diary", `{"body":""}`); rec.Code != 422 {
		t.Fatalf("empty: want 422 got %d", rec.Code)
	}
	// плохой mood → 422
	if rec := do(tok, "POST", "/v1/diary", `{"body":"x","mood":"weird"}`); rec.Code != 422 {
		t.Fatalf("mood: want 422 got %d", rec.Code)
	}
	// создать две (одна calm)
	rec := do(tok, "POST", "/v1/diary", `{"body":"первая","mood":"calm"}`)
	if rec.Code != 200 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var e1 map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &e1)
	if e1["id"] == nil || e1["mood"] != "calm" {
		t.Fatalf("bad entry: %s", rec.Body.String())
	}
	if rec := do(tok, "POST", "/v1/diary", `{"body":"вторая"}`); rec.Code != 200 {
		t.Fatalf("create2: %d", rec.Code)
	}
	// list все
	lrec := do(tok, "GET", "/v1/diary?limit=20", "")
	var list []map[string]any
	if err := json.Unmarshal(lrec.Body.Bytes(), &list); err != nil || len(list) != 2 {
		t.Fatalf("list: %s", lrec.Body.String())
	}
	// фильтр mood
	frec := do(tok, "GET", "/v1/diary?mood=calm", "")
	var filtered []map[string]any
	if err := json.Unmarshal(frec.Body.Bytes(), &filtered); err != nil || len(filtered) != 1 {
		t.Fatalf("filter: %s", frec.Body.String())
	}
	// get одной
	id := e1["id"].(string)
	if grec := do(tok, "GET", "/v1/diary/"+id, ""); grec.Code != 200 {
		t.Fatalf("get: %d", grec.Code)
	}
	// export
	if xrec := do(tok, "POST", "/v1/diary/export", ""); xrec.Code != 200 {
		t.Fatalf("export: %d", xrec.Code)
	} else {
		var all []map[string]any
		if err := json.Unmarshal(xrec.Body.Bytes(), &all); err != nil || len(all) != 2 {
			t.Fatalf("export body: %s", xrec.Body.String())
		}
	}
}
