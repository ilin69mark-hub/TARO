// E2E readings-хендлеры против живых PG/Redis (см. D-покрытие, 02-functional/03).
// Идем через chi-роутер + RequireAuth с живой cookie (как cmd/api) — userCtxKey недоступен снаружи auth.
package readings

import (
	"encoding/json"
	"fmt"
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

// testClient строит chi-роутер чтений за RequireAuth и возвращает caller с живой cookie.
func testClient(t *testing.T) (func(method, path, body string, headers map[string]string) *httptest.ResponseRecorder, string) {
	t.Helper()
	ctx, pg, rd := testutil.Live(t)
	en := entitlements.New(pg, rd)
	gw := ai.New(pg, rd) // без ключа → fallback
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
	r.With(au.RequireAuth).Get("/v1/readings", svc.HandleList)
	r.With(au.RequireAuth).Get("/v1/readings/{id}", svc.HandleGet)
	r.With(au.RequireAuth).Get("/v1/streak/me", svc.HandleStreak)
	do := func(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	return do, uid
}

func TestE2EReadingFlow(t *testing.T) {
	do, _ := testClient(t)
	h := map[string]string{"Idempotency-Key": "rk-1"}

	rec := do("POST", "/v1/readings", `{"spread_code":"daily","question":"Как день?"}`, h)
	if rec.Code != 200 {
		t.Fatalf("create: want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	var created map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created["reading_id"] == "" {
		t.Fatalf("bad create body: %s", rec.Body.String())
	}
	id := created["reading_id"]

	// same key → same id, лимит не тронут
	rec2 := do("POST", "/v1/readings", `{"spread_code":"daily"}`, h)
	var again map[string]string
	_ = json.Unmarshal(rec2.Body.Bytes(), &again)
	if again["reading_id"] != id {
		t.Fatalf("idempotency broken: %s vs %s", again["reading_id"], id)
	}

	// второй ключ → 402 (лимит 1/день)
	rec3 := do("POST", "/v1/readings", `{"spread_code":"three"}`, map[string]string{"Idempotency-Key": "rk-2"})
	if rec3.Code != 402 {
		t.Fatalf("second: want 402 got %d", rec3.Code)
	}

	// GET: карты обогащены, locked=false сегодня
	grec := do("GET", "/v1/readings/"+id, "", nil)
	if grec.Code != 200 {
		t.Fatalf("get: want 200 got %d: %s", grec.Code, grec.Body.String())
	}
	var got struct {
		Cards          []map[string]any `json:"cards"`
		Interpretation string           `json:"interpretation"`
		Locked         bool             `json:"locked"`
	}
	if err := json.Unmarshal(grec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Cards) != 1 || got.Cards[0]["name_ru"] == nil || got.Interpretation == "" || got.Locked {
		t.Fatalf("bad get body: %s", grec.Body.String()[:min(200, len(grec.Body.String()))])
	}

	// list содержит чтение
	lrec := do("GET", "/v1/readings?limit=20", "", nil)
	var list []map[string]any
	if err := json.Unmarshal(lrec.Body.Bytes(), &list); err != nil || len(list) != 1 {
		t.Fatalf("bad list: %s", lrec.Body.String())
	}
}

func TestE2ECrisisNoConsume(t *testing.T) {
	do, _ := testClient(t)
	rec := do("POST", "/v1/readings", `{"spread_code":"daily","question":"хочу покончить с собой"}`,
		map[string]string{"Idempotency-Key": "cr-1"})
	if rec.Code != 200 {
		t.Fatalf("crisis create: want 200 got %d", rec.Code)
	}
	// лимит цел: обычное чтение проходит
	rec2 := do("POST", "/v1/readings", `{"spread_code":"daily"}`,
		map[string]string{"Idempotency-Key": "cr-2"})
	if rec2.Code != 200 {
		t.Fatalf("crisis consumed limit: got %d", rec2.Code)
	}
}

func TestE2ESSEReplay(t *testing.T) {
	do, _ := testClient(t)
	rec := do("POST", "/v1/readings", `{"spread_code":"daily"}`,
		map[string]string{"Idempotency-Key": "sse-1", "Accept": "text/event-stream"})
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `"done":true`) || !strings.Contains(body, "data:") {
		t.Fatalf("SSE broken: %d %q", rec.Code, body[:min(120, len(body))])
	}
}

func TestE2EStreamLive(t *testing.T) {
	// живой SSE-стрим через мок-OpenRouter (см. streamLive, D-покрытие)
	t.Setenv("OPENROUTER_API_KEY", "test-key-0123456789abcdef")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"Живой "}}]}`)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"токен."}}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	do, _ := testClient(t)
	rec := do("POST", "/v1/readings", `{"spread_code":"daily"}`,
		map[string]string{"Idempotency-Key": "live-1", "Accept": "text/event-stream"})
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Живой") || !strings.Contains(body, `"done":true`) {
		t.Fatalf("live SSE: %d %q", rec.Code, body[:min(160, len(body))])
	}
}

func TestE2EStreakEndpoint(t *testing.T) {
	do, _ := testClient(t)
	// чтение сегодня → стрик 1
	if rec := do("POST", "/v1/readings", `{"spread_code":"daily"}`,
		map[string]string{"Idempotency-Key": "streak-1"}); rec.Code != 200 {
		t.Fatalf("create: %d", rec.Code)
	}
	rec := do("GET", "/v1/streak/me", "", nil)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out["days"] != float64(1) {
		t.Fatalf("streak: %s", rec.Body.String())
	}
}

func TestE2EGenerateFallback(t *testing.T) {
	// generate без ключа: fallback done (gw.Enabled()==false в этом окружении)
	ctx, pg, rd := testutil.Live(t)
	en := entitlements.New(pg, rd)
	gw := ai.New(pg, rd)
	rf := referral.New(pg, en)
	svc := New(pg, en, gw, rf)
	uid := testutil.NewUser(t, ctx, pg)
	cards := draw(99, 1)
	svc.generate(ctx, "00000000-0000-0000-0000-000000000000", "daily", "", cards)
	_ = uid
	// aiInputs собирает позиции+значения
	pos, vals, name := svc.aiInputs(ctx, "daily", cards)
	if name == "" || len(pos) != 1 || len(vals) != 1 || vals[0].Name == "" {
		t.Fatalf("aiInputs: %q %d %d", name, len(pos), len(vals))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
