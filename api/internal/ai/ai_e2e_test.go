// E2E AI-гейтвея против мок-OpenRouter (см. D-покрытие, 04-architecture/06).
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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
	t.Setenv("OPENROUTER_API_KEY", "test-key-0123456789abcdef")
	// e2e-тесты делят dev-Redis: чистим AI-ключи чтобы не ловить чужой кэш/breaker
	iter := rd.Scan(ctx, 0, "ai:*", 100).Iterator()
	for iter.Next(ctx) {
		_ = rd.Del(ctx, iter.Val()).Err()
	}
	gw := New(pg, rd)
	return ctx, pg, rd, gw
}

var testCards = []CardValue{{Name: "Маг", Upright: "Воля", ReversedText: "Сомнение", CardID: 1}}
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
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")

	ch := make(chan string, 64)
	text, model, cached, err := gw.Stream(ctx, "", "daily", testPos, testCards, "Вопрос?", ch)
	drain(ch)
	if err != nil || text == "" || cached || model == "" || len(seen) == 0 {
		t.Fatalf("stream: text=%q cached=%v err=%v seen=%d", text, cached, err, len(seen))
	}
	// повтор с ТЕМ ЖЕ вопросом — из кэша, без провайдера (см. S04: вопрос входит в ключ)
	n := len(seen)
	ch2 := make(chan string, 64)
	text2, _, cached2, err := gw.Stream(ctx, "", "daily", testPos, testCards, "Вопрос?", ch2)
	drain(ch2)
	if err != nil || !cached2 || text2 != text || len(seen) != n {
		t.Fatalf("cache: cached=%v err=%v calls=%d", cached2, err, len(seen))
	}
	// ДРУГОЙ вопрос — свежий вызов провайдера (S04: было PII-leak)
	ch3 := make(chan string, 64)
	_, _, cached3, err := gw.Stream(ctx, "", "daily", testPos, testCards, "ДРУГОЙ вопрос?", ch3)
	drain(ch3)
	if err != nil || cached3 {
		t.Fatalf("other question must miss cache: cached=%v err=%v", cached3, err)
	}
}

func TestE2EStreamBothDown(t *testing.T) {
	ctx, pg, _, gw := testGateway(t)
	var seen []string
	srv := mockServer(t, "500", &seen)
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")

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
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")

	// кладем pending_fallback вручную
	var uid string
	if err := pg.QueryRow(ctx,
		`INSERT INTO users (anon_uuid) VALUES (gen_random_uuid()) RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, uid) })
	var rid string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, seed, status, interpretation, quota_state)
		VALUES ($1,'daily','', '[{"card_id":1,"reversed":false,"position":0}]',1,'pending_fallback','','allowed')
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
	done := gw.startWorker(cctx, pg, 20*time.Millisecond)
	time.Sleep(80 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestE2EStreamPanicChannelOwnership(t *testing.T) {
	ctx, pg, _, gw := testGateway(t)
	var old json.RawMessage
	if err := pg.QueryRow(ctx, `SELECT value FROM app_config WHERE key='ai'`).Scan(&old); err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal(map[string]any{
		"model": "test/panic", "fallback": "test/panic", "max_tokens": 32,
		"temperature": 0, "monthly_calls": 1000000,
	})
	if _, err := pg.Exec(ctx, `UPDATE app_config SET value=$1 WHERE key='ai'`, value); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pg.Exec(context.Background(), `UPDATE app_config SET value=$1 WHERE key='ai'`, old) })
	gw.http = &http.Client{Transport: regressionPanicTransport{}}
	ch := make(chan string, 4)
	result := make(chan error, 1)
	go func() {
		_, _, _, err := gw.Stream(ctx, "", "daily", testPos, testCards, "panic", ch)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("panic must return an error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("panic left stream blocked")
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("closed output must not yield a value")
		}
	case <-time.After(time.Second):
		t.Fatal("output channel was not closed")
	}
}

func TestE2EModerationBeforeCache(t *testing.T) {
	ctx, _, rd, gw := testGateway(t)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseProvider := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseProvider()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"порча"}}]}`)
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		<-release
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")
	ch := make(chan string, 8)
	type result struct {
		text string
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		text, _, _, err := gw.Stream(ctx, "", "daily", testPos, testCards, "moderation", ch)
		resultCh <- result{text, err}
	}()
	select {
	case token, ok := <-ch:
		if ok {
			t.Fatalf("raw token escaped before moderation: %q", token)
		}
		result := <-resultCh
		t.Fatalf("stream ended before moderation: %v", result.err)
	case <-time.After(100 * time.Millisecond):
	}
	releaseProvider()
	got := <-resultCh
	if got.err != nil || got.text != SafeReplacement {
		t.Fatalf("stream=%q err=%v", got.text, got.err)
	}
	var streamed strings.Builder
	for token := range ch {
		streamed.WriteString(token)
	}
	if strings.Contains(streamed.String(), "порча") || !strings.Contains(streamed.String(), SafeReplacement) {
		t.Fatalf("unsafe output: %q", streamed.String())
	}
	cfg := gw.LoadConfig(ctx)
	raw, err := rd.Get(ctx, cacheKey(cfg.Model, "daily", testPos, testCards, "moderation", cfg)).Result()
	if err != nil {
		t.Fatal(err)
	}
	cached, err := openCache(raw)
	if err != nil || cached != SafeReplacement || strings.Contains(cached, "порча") {
		t.Fatalf("cache=%q err=%v", cached, err)
	}
}

func TestE2EBudgetReservationRace(t *testing.T) {
	ctx, pg, _, gw := testGateway(t)
	model := fmt.Sprintf("test/budget-%d", time.Now().UnixNano())
	var currentBudget int
	if err := pg.QueryRow(ctx, `
		SELECT COUNT(*) FROM ai_budget_ledger
		 WHERE month_start=date_trunc('month', now())::date
		   AND status IN ('reserved', 'consumed')`).Scan(&currentBudget); err != nil {
		t.Fatal(err)
	}
	var old json.RawMessage
	if err := pg.QueryRow(ctx, `SELECT value FROM app_config WHERE key='ai'`).Scan(&old); err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal(map[string]any{
		"model": model, "fallback": model, "max_tokens": 32,
		"temperature": 0, "monthly_calls": currentBudget + 1,
	})
	if _, err := pg.Exec(ctx, `UPDATE app_config SET value=$1 WHERE key='ai'`, value); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM ai_budget_ledger WHERE model=$1`, model)
		_, _ = pg.Exec(context.Background(), `UPDATE app_config SET value=$1 WHERE key='ai'`, old)
	})
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"safe"}}]}`)
		fmt.Fprintln(w, `data: {"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4,"cost":0.001}}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")
	type outcome struct {
		err error
	}
	outcomes := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			ch := make(chan string, 8)
			_, _, _, err := gw.Stream(ctx, "", "daily", testPos, testCards, fmt.Sprintf("budget-%d", i), ch)
			outcomes <- outcome{err}
		}(i)
	}
	successes, exhausted := 0, 0
	for i := 0; i < 2; i++ {
		result := <-outcomes
		if result.err == nil {
			successes++
		} else if errors.Is(result.err, ErrBudgetExhausted) {
			exhausted++
		}
	}
	if successes != 1 || exhausted != 1 || calls.Load() != 1 {
		t.Fatalf("successes=%d exhausted=%d provider_calls=%d", successes, exhausted, calls.Load())
	}
}

func TestE2EWorkerClaimIsIdempotent(t *testing.T) {
	ctx, pg, _, gw := testGateway(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"worker"}}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")
	var uid string
	if err := pg.QueryRow(ctx, `INSERT INTO users (anon_uuid) VALUES (gen_random_uuid()) RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, uid) })
	var rid string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, seed, status, interpretation, quota_state)
		VALUES ($1,'daily','', '[{"card_id":1,"reversed":false,"position":0}]',1,'pending','','allowed')
		RETURNING id`, uid).Scan(&rid); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			gw.drainOnce(ctx, pg)
		}()
	}
	wg.Wait()
	var status string
	var attempts int
	if err := pg.QueryRow(ctx, `SELECT status, worker_attempts FROM readings WHERE id=$1`, rid).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "done" || attempts != 1 || calls.Load() != 1 {
		t.Fatalf("status=%s attempts=%d calls=%d", status, attempts, calls.Load())
	}
}

func TestE2EWorkerClaimsBoundedBatch(t *testing.T) {
	ctx, pg, _, gw := testGateway(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"bounded"}}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")
	var uid string
	if err := pg.QueryRow(ctx, `INSERT INTO users (anon_uuid) VALUES (gen_random_uuid()) RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, uid) })
	ids := make([]string, 2)
	for i := range ids {
		if err := pg.QueryRow(ctx, `
			INSERT INTO readings (user_id, spread_code, question, cards, seed, status, quota_state, updated_at)
			VALUES ($1,'daily','', '[{"card_id":1,"reversed":false,"position":0}]',1,'pending','allowed',now()-interval '100 years')
			RETURNING id`, uid).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	gw.drainOnce(ctx, pg)
	var done, pending, attempts int
	if err := pg.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE status='done'),
		       COUNT(*) FILTER (WHERE status='pending'),
		       COALESCE(SUM(worker_attempts),0)
		  FROM readings WHERE id=ANY($1)`, ids).Scan(&done, &pending, &attempts); err != nil {
		t.Fatal(err)
	}
	if done != 1 || pending != 1 || attempts != 1 || calls.Load() < 1 {
		t.Fatalf("done=%d pending=%d attempts=%d calls=%d", done, pending, attempts, calls.Load())
	}
}

func setAIConfig(t *testing.T, ctx context.Context, pg *pgxpool.Pool, model, fallback string, monthly int) {
	t.Helper()
	var old json.RawMessage
	if err := pg.QueryRow(ctx, `SELECT value FROM app_config WHERE key='ai'`).Scan(&old); err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal(map[string]any{
		"model": model, "fallback": fallback, "max_tokens": 64,
		"temperature": 0, "monthly_calls": monthly,
	})
	if _, err := pg.Exec(ctx, `UPDATE app_config SET value=$1 WHERE key='ai'`, value); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM ai_budget_ledger WHERE model=$1 OR model=$2`, model, fallback)
		_, _ = pg.Exec(context.Background(), `UPDATE app_config SET value=$1 WHERE key='ai'`, old)
	})
}

func TestE2EFallbackCacheUsesFallbackIdentity(t *testing.T) {
	ctx, pg, rd, gw := testGateway(t)
	primary := fmt.Sprintf("test/primary-%d", time.Now().UnixNano())
	fallback := fmt.Sprintf("test/fallback-%d", time.Now().UnixNano())
	setAIConfig(t, ctx, pg, primary, fallback, 1000000)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"fallback answer"}}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")
	ch := make(chan string, 16)
	text, model, cached, err := gw.Stream(ctx, "", "daily", testPos, testCards, "fallback-identity", ch)
	drain(ch)
	if err != nil || text != "fallback answer" || model != fallback || cached {
		t.Fatalf("text=%q model=%q cached=%v err=%v", text, model, cached, err)
	}
	cfg := gw.LoadConfig(ctx)
	primaryKey := cacheKey(primary, "daily", testPos, testCards, "fallback-identity", cfg)
	fallbackKey := cacheKey(fallback, "daily", testPos, testCards, "fallback-identity", cfg)
	if n, _ := rd.Exists(ctx, primaryKey).Result(); n != 0 {
		t.Fatal("fallback response was cached under primary identity")
	}
	if n, _ := rd.Exists(ctx, fallbackKey).Result(); n != 1 {
		t.Fatal("fallback response was not cached under fallback identity")
	}
	ch2 := make(chan string, 16)
	text2, model2, cached2, err := gw.Stream(ctx, "", "daily", testPos, testCards, "fallback-identity", ch2)
	drain(ch2)
	if err != nil || text2 != text || model2 != fallback || !cached2 || calls.Load() != 2 {
		t.Fatalf("cache hit text=%q model=%q cached=%v calls=%d err=%v", text2, model2, cached2, calls.Load(), err)
	}
}

func TestE2EOpenBreakerDoesNotReserveBudget(t *testing.T) {
	ctx, pg, rd, gw := testGateway(t)
	model := fmt.Sprintf("test/breaker-%d", time.Now().UnixNano())
	setAIConfig(t, ctx, pg, model, model, 1000000)
	if err := rd.Set(ctx, breakerKey(model), "open", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")
	ch := make(chan string, 8)
	_, _, _, err := gw.Stream(ctx, "", "daily", testPos, testCards, "breaker-budget", ch)
	drain(ch)
	if err == nil || calls.Load() != 0 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
	var reservations int
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM ai_budget_ledger WHERE model=$1`, model).Scan(&reservations); err != nil {
		t.Fatal(err)
	}
	if reservations != 0 {
		t.Fatalf("open breaker reserved %d budget rows", reservations)
	}
}

func TestE2EWorkerSkipsUnverifiedQuotaAndBoundsRetries(t *testing.T) {
	ctx, pg, _, gw := testGateway(t)
	var uid string
	if err := pg.QueryRow(ctx, `INSERT INTO users (anon_uuid) VALUES (gen_random_uuid()) RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, uid) })
	var unverified string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, seed, status, interpretation, quota_state)
		VALUES ($1,'daily','unchecked','[{"card_id":1,"reversed":false,"position":0}]',1,'pending','','error')
		RETURNING id`, uid).Scan(&unverified); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"partial"}}]}`)
	}))
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")
	gw.drainOnce(ctx, pg)
	var status, quota string
	var attempts int
	if err := pg.QueryRow(ctx, `SELECT status, quota_state, worker_attempts FROM readings WHERE id=$1`, unverified).Scan(&status, &quota, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || quota != "error" || attempts != 0 || calls.Load() != 0 {
		t.Fatalf("unverified row status=%s quota=%s attempts=%d calls=%d", status, quota, attempts, calls.Load())
	}
	model := fmt.Sprintf("test/retry-%d", time.Now().UnixNano())
	setAIConfig(t, ctx, pg, model, model, 1000000)
	t.Setenv("AI_WORKER_MAX_ATTEMPTS", "2")
	t.Setenv("AI_WORKER_BACKOFF_SECONDS", "1")
	t.Setenv("AI_WORKER_MAX_BACKOFF_SECONDS", "1")
	var rid string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, seed, status, interpretation, quota_state)
		VALUES ($1,'daily','retry','[{"card_id":1,"reversed":false,"position":0}]',1,'pending','','allowed')
		RETURNING id`, uid).Scan(&rid); err != nil {
		t.Fatal(err)
	}
	gw.drainOnce(ctx, pg)
	if _, err := pg.Exec(ctx, `UPDATE readings SET worker_lease_until=now()-interval '1 second' WHERE id=$1`, rid); err != nil {
		t.Fatal(err)
	}
	gw.drainOnce(ctx, pg)
	if err := pg.QueryRow(ctx, `SELECT status, worker_attempts FROM readings WHERE id=$1`, rid).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || attempts != 2 || calls.Load() != 2 {
		t.Fatalf("retry row status=%s attempts=%d calls=%d", status, attempts, calls.Load())
	}
}

func TestE2EWorkerPersistsTerminalFallbackWithoutAI(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	t.Setenv("OPENROUTER_API_KEY", "")
	gw := New(pg, rd)
	if gw.Enabled() {
		t.Fatal("gateway must be disabled")
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"disabled"}}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")
	var uid string
	if err := pg.QueryRow(ctx, `INSERT INTO users (anon_uuid) VALUES (gen_random_uuid()) RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, uid) })
	var rid string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, seed, status, interpretation, quota_state)
		VALUES ($1,'daily','', '[{"card_id":1,"reversed":false,"position":0}]',1,'pending','','allowed')
		RETURNING id`, uid).Scan(&rid); err != nil {
		t.Fatal(err)
	}
	gw.drainOnce(ctx, pg)
	var status, interpretation string
	var attempts int
	if err := pg.QueryRow(ctx, `SELECT status, interpretation, worker_attempts FROM readings WHERE id=$1`, rid).Scan(&status, &interpretation, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "done" || interpretation == "" || attempts != 1 || calls.Load() != 0 {
		t.Fatalf("status=%s interpretation=%q attempts=%d calls=%d", status, interpretation, attempts, calls.Load())
	}
}

func TestE2EWorkerPersistsTerminalFallbackForIncompleteContext(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	gw := New(pg, rd)
	t.Setenv("OPENROUTER_API_KEY", "test-key-0123456789abcdef")
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"incomplete"}}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")
	var uid string
	if err := pg.QueryRow(ctx, `INSERT INTO users (anon_uuid) VALUES (gen_random_uuid()) RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, uid) })
	var rid string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, seed, status, interpretation, quota_state)
		VALUES ($1,'daily','',
			'[{"card_id":1,"reversed":false,"position":0},{"card_id":2,"reversed":true,"position":1}]',
			1,'pending','','allowed')
		RETURNING id`, uid).Scan(&rid); err != nil {
		t.Fatal(err)
	}
	gw.drainOnce(ctx, pg)
	var status, interpretation string
	var attempts int
	if err := pg.QueryRow(ctx, `SELECT status, interpretation, worker_attempts FROM readings WHERE id=$1`, rid).Scan(&status, &interpretation, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "done" || interpretation == "" || attempts != 1 || calls.Load() != 0 {
		t.Fatalf("status=%s interpretation=%q attempts=%d calls=%d", status, interpretation, attempts, calls.Load())
	}
	gw.drainOnce(ctx, pg)
	if err := pg.QueryRow(ctx, `SELECT status, worker_attempts FROM readings WHERE id=$1`, rid).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "done" || attempts != 1 {
		t.Fatalf("terminal fallback was requeued: status=%s attempts=%d", status, attempts)
	}
}

func TestE2EWorkerReclaimsOnlyExpiredClaims(t *testing.T) {
	ctx, pg, _, gw := testGateway(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"reclaimed"}}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")
	var uid string
	if err := pg.QueryRow(ctx, `INSERT INTO users (anon_uuid) VALUES (gen_random_uuid()) RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, uid) })
	activeToken := "11111111-1111-4111-8111-111111111111"
	expiredToken := "22222222-2222-4222-8222-222222222222"
	insert := func(key, token string, lease time.Duration) string {
		t.Helper()
		var id string
		if err := pg.QueryRow(ctx, `
			INSERT INTO readings (user_id, spread_code, question, cards, interpretation, seed, status, idempotency_key, quota_state, worker_claim_token, worker_lease_until, worker_attempts)
			VALUES ($1,'daily','', '[{"card_id":1,"reversed":false,"position":0}]','',1,'pending',$2,'allowed',$3,now()+$4::interval,1)
			RETURNING id`, uid, key, token, lease.String()).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	activeID := insert("worker-active-claim", activeToken, 2*time.Minute)
	expiredID := insert("worker-expired-claim", expiredToken, -time.Second)
	gw.drainOnce(ctx, pg)
	var activeStatus, activeTokenAfter, expiredStatus, expiredTokenAfter string
	var activeAttempts, expiredAttempts int
	if err := pg.QueryRow(ctx, `
		SELECT status, worker_attempts, worker_claim_token::text FROM readings WHERE id=$1`, activeID).Scan(&activeStatus, &activeAttempts, &activeTokenAfter); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `
		SELECT status, worker_attempts, COALESCE(worker_claim_token::text, '')
		  FROM readings WHERE id=$1`, expiredID).Scan(&expiredStatus, &expiredAttempts, &expiredTokenAfter); err != nil {
		t.Fatal(err)
	}
	if activeStatus != "pending" || activeAttempts != 1 || activeTokenAfter != activeToken {
		t.Fatalf("active claim status=%s attempts=%d token=%s", activeStatus, activeAttempts, activeTokenAfter)
	}
	if expiredStatus != "done" || expiredAttempts != 2 || expiredTokenAfter != "" {
		t.Fatalf("expired claim status=%s attempts=%d token=%s", expiredStatus, expiredAttempts, expiredTokenAfter)
	}
}
