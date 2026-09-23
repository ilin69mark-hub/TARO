// E2E AI-гейтвея против мок-OpenRouter (см. D-покрытие, 04-architecture/06).
package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"taro/api/internal/testutil"
)

// mockServer отдает SSE-чанки OpenRouter-формата. mode: ok | 500.
func mockServer(t *testing.T, mode string, seen *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		if mode == "500" {
			w.WriteHeader(500)
			return
		}
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"Карты "}}]}`)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"говорят."}}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
}

func testGateway(t *testing.T) (context.Context, *pgxpool.Pool, *redis.Client, *Gateway) {
	t.Helper()
	ctx, pg, rd := testutil.Live(t)
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	// e2e-тесты делят dev-Redis: чистим AI-ключи чтобы не ловить чужой кэш/breaker
	iter := rd.Scan(ctx, 0, "ai:*", 100).Iterator()
	for iter.Next(ctx) {
		_ = rd.Del(ctx, iter.Val()).Err()
	}
	gw := New(pg, rd)
	return ctx, pg, rd, gw
}

var testCards = []CardValue{{Name: "Маг", Upright: "Воля", CardID: 1}}
var testPos = []Position{{Label: "Карта дня", Meaning: "фокус"}}

func drain(ch <-chan string) {
	for range ch {
	}
}

func TestE2EStreamOKCache(t *testing.T) {
	ctx, _, _, gw := testGateway(t)
	var seen []string
	srv := mockServer(t, "ok", &seen)
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)

	ch := make(chan string, 64)
	text, model, cached, err := gw.Stream(ctx, "", "daily", testPos, testCards, "Вопрос?", ch)
	drain(ch)
	if err != nil || text == "" || cached || model == "" || len(seen) == 0 {
		t.Fatalf("stream: text=%q cached=%v err=%v seen=%d", text, cached, err, len(seen))
	}
	// повтор с ДРУГИМ вопросом — из кэша (вопрос не входит в ключ), без провайдера
	n := len(seen)
	ch2 := make(chan string, 64)
	text2, _, cached2, err := gw.Stream(ctx, "", "daily", testPos, testCards, "ДРУГОЙ вопрос?", ch2)
	drain(ch2)
	if err != nil || !cached2 || text2 != text || len(seen) != n {
		t.Fatalf("cache: cached=%v err=%v calls=%d", cached2, err, len(seen))
	}
}

func TestE2EStreamBothDown(t *testing.T) {
	ctx, pg, _, gw := testGateway(t)
	var seen []string
	srv := mockServer(t, "500", &seen)
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)

	ch := make(chan string, 64)
	_, _, _, err := gw.Stream(ctx, "", "daily", testPos, testCards, "", ch)
	drain(ch)
	if err == nil {
		t.Fatal("want error when both models 500")
	}
	var logs int
	_ = pg.QueryRow(ctx, `SELECT COUNT(*) FROM ai_logs WHERE status='failed'`).Scan(&logs)
	if logs < 2 {
		t.Fatalf("want >=2 failed ai_logs, got %d", logs)
	}
}

func TestE2EWorkerDrain(t *testing.T) {
	ctx, pg, _, gw := testGateway(t)
	var seen []string
	srv := mockServer(t, "ok", &seen)
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)

	// кладем pending_fallback вручную
	var uid string
	if err := pg.QueryRow(ctx,
		`INSERT INTO users (anon_uuid) VALUES (gen_random_uuid()) RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, uid) })
	var rid string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, seed, status, interpretation)
		VALUES ($1,'daily','', '[{"card_id":1,"reversed":false,"position":0}]',1,'pending_fallback','')
		RETURNING id`, uid).Scan(&rid); err != nil {
		t.Fatal(err)
	}
	gw.drainOnce(ctx, pg)
	var status, interp string
	_ = pg.QueryRow(ctx, `SELECT status, interpretation FROM readings WHERE id=$1`, rid).Scan(&status, &interp)
	if status != "done" || interp == "" {
		t.Fatalf("worker: status=%s interp=%q", status, interp)
	}
}

func TestE2EStartWorkerTick(t *testing.T) {
	ctx, pg, _, gw := testGateway(t)
	cctx, cancel := context.WithCancel(ctx)
	gw.StartWorker(cctx, pg, 20*time.Millisecond)
	time.Sleep(80 * time.Millisecond)
	cancel()
}
