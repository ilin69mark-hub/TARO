// E2E readings-хендлеры против живых PG/Redis (см. D-покрытие, 02-functional/03).
// Идем через chi-роутер + RequireAuth с живой cookie (как cmd/api) — userCtxKey недоступен снаружи auth.
package readings

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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
	rec2 := do("POST", "/v1/readings", `{"spread_code":"daily","question":"Как день?"}`, h)
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
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")
	do, _ := testClient(t)
	rec := do("POST", "/v1/readings", `{"spread_code":"daily"}`,
		map[string]string{"Idempotency-Key": "live-1", "Accept": "text/event-stream"})
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "Живой") || !strings.Contains(body, `"done":true`) {
		t.Fatalf("live SSE: %d %q", rec.Code, body[:min(160, len(body))])
	}
}

func TestE2ETruncatedProviderIsNotPersistedDone(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	t.Setenv("OPENROUTER_API_KEY", "test-key-0123456789abcdef")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"partial"}}]}`)
	}))
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")
	svc := New(pg, entitlements.New(pg, rd), ai.New(pg, rd), nil)
	uid := testutil.NewUser(t, ctx, pg)
	cards := draw(17, 1)
	cardsJSON, _ := json.Marshal(cards)
	var id string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, seed, status, interpretation, quota_state, worker_lease_until)
		VALUES ($1,'daily','',$2,1,'pending','','allowed',now()+interval '2 minutes') RETURNING id`, uid, cardsJSON).Scan(&id); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/readings", nil)
	rec := httptest.NewRecorder()
	svc.streamLive(rec, req, id, "daily", "truncated", cards)
	var status string
	if err := pg.QueryRow(ctx, `SELECT status FROM readings WHERE id=$1`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status == "done" || status != "pending_fallback" {
		t.Fatalf("truncated stream status=%s body=%s", status, rec.Body.String())
	}
}

func TestE2EStreamCancellationPersistsFallback(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	t.Setenv("OPENROUTER_API_KEY", "test-key-0123456789abcdef")
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseProvider := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseProvider()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")
	en := entitlements.New(pg, rd)
	gw := ai.New(pg, rd)
	svc := New(pg, en, gw, nil)
	uid := testutil.NewUser(t, ctx, pg)
	question := fmt.Sprintf("cancel-%d", time.Now().UnixNano())
	cards := draw(101, 1)
	cardsJSON, _ := json.Marshal(cards)
	var id string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, seed, status, interpretation, quota_state, worker_lease_until)
		VALUES ($1,'daily','',$2,1,'pending','','allowed',now()+interval '2 minutes') RETURNING id`, uid, cardsJSON).Scan(&id); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/readings", nil)
	reqCtx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(reqCtx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		svc.streamLive(rec, req, id, "daily", question, cards)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("provider request did not start")
	}
	cancel()
	releaseProvider()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not finish after cancellation")
	}
	var status, interpretation string
	if err := pg.QueryRow(ctx, `SELECT status, interpretation FROM readings WHERE id=$1`, id).Scan(&status, &interpretation); err != nil {
		t.Fatal(err)
	}
	if status != "pending_fallback" || interpretation == "" {
		t.Fatalf("status=%s interpretation=%q", status, interpretation)
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

func TestE2EPrepareReadingWithMaxConnsOne(t *testing.T) {
	ctx, _, rd := testutil.Live(t)
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	pg, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pg.Close)
	uid := testutil.NewUser(t, ctx, pg)
	svc := New(pg, entitlements.New(pg, rd), ai.New(pg, rd), nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/readings", nil)
	rec := httptest.NewRecorder()
	id, _, _, ok := svc.prepareReading(rec, req, uid, "max-one", createRequest{SpreadCode: "daily", Question: "one connection"})
	if !ok || id == "" {
		t.Fatalf("prepare failed: id=%q body=%s", id, rec.Body.String())
	}
	var status, quota string
	if err := pg.QueryRow(ctx, `SELECT status, quota_state FROM readings WHERE id=$1`, id).Scan(&status, &quota); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || quota != "allowed" {
		t.Fatalf("status=%s quota=%s", status, quota)
	}
}

func TestE2EFailedSingleReadingRetryReusesEntitlement(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, entitlements.New(pg, rd), ai.New(pg, rd), nil)
	for _, status := range []string{"failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			uid := testutil.NewUser(t, ctx, pg)
			cardsJSON, _ := json.Marshal(draw(23, 5))
			quotaState := "error"
			if status == "cancelled" {
				quotaState = "denied"
			}
			key := "single-retry-" + status
			var readingID string
			if err := pg.QueryRow(ctx, `
				INSERT INTO readings (user_id, spread_code, question, cards, interpretation, seed, status, idempotency_key, quota_state)
				VALUES ($1,'decision','retry',$2,'',1,$3,$4,$5)
				RETURNING id`, uid, cardsJSON, status, key, quotaState).Scan(&readingID); err != nil {
				t.Fatal(err)
			}
			paymentIDs := make([]string, 2)
			for i := range paymentIDs {
				if err := pg.QueryRow(ctx, `
					INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status)
					SELECT $1, id, 'single_99', 99, 'tg_stars', 'retry:' || gen_random_uuid(), 99, 66, 'succeeded'
					  FROM plans WHERE code='single_99' ORDER BY valid_from DESC LIMIT 1
					RETURNING id`, uid).Scan(&paymentIDs[i]); err != nil {
					t.Fatal(err)
				}
			}
			var linkedSingleID, spareSingleID string
			if err := pg.QueryRow(ctx, `
				INSERT INTO single_entitlements (user_id, spread_code, payment_id, consumed_reading_id)
				VALUES ($1,'decision',$2,$3) RETURNING id::text`, uid, paymentIDs[0], readingID).Scan(&linkedSingleID); err != nil {
				t.Fatal(err)
			}
			if err := pg.QueryRow(ctx, `
				INSERT INTO single_entitlements (user_id, spread_code, payment_id)
				VALUES ($1,'decision',$2) RETURNING id::text`, uid, paymentIDs[1]).Scan(&spareSingleID); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/readings", nil)
			rec := httptest.NewRecorder()
			id, verdict, retriedCards, ok := svc.prepareReading(rec, req, uid, key, createRequest{SpreadCode: "decision", Question: "retry"})
			if !ok || id != readingID || verdict.Reason != "single" || verdict.SingleID != linkedSingleID || len(retriedCards) != 5 {
				t.Fatalf("retry id=%q verdict=%+v cards=%d ok=%v body=%s", id, verdict, len(retriedCards), ok, rec.Body.String())
			}
			var status, quota string
			if err := pg.QueryRow(ctx, `SELECT status, quota_state FROM readings WHERE id=$1`, readingID).Scan(&status, &quota); err != nil {
				t.Fatal(err)
			}
			var total, linked, spare int
			if err := pg.QueryRow(ctx, `
				SELECT COUNT(*),
				       COUNT(*) FILTER (WHERE consumed_reading_id=$2),
				       COUNT(*) FILTER (WHERE consumed_reading_id IS NULL)
				  FROM single_entitlements WHERE user_id=$1`, uid, readingID).Scan(&total, &linked, &spare); err != nil {
				t.Fatal(err)
			}
			var readingCount int
			if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM readings WHERE id=$1`, readingID).Scan(&readingCount); err != nil {
				t.Fatal(err)
			}
			if status != "pending" || quota != "allowed" || total != 2 || linked != 1 || spare != 1 || readingCount != 1 {
				t.Fatalf("status=%s quota=%s entitlements=%d linked=%d spare=%d readings=%d", status, quota, total, linked, spare, readingCount)
			}
			var spareReading string
			if err := pg.QueryRow(ctx, `SELECT COALESCE(consumed_reading_id::text, '') FROM single_entitlements WHERE id=$1`, spareSingleID).Scan(&spareReading); err != nil {
				t.Fatal(err)
			}
			if spareReading != "" {
				t.Fatalf("spare entitlement consumed by %s", spareReading)
			}
		})
	}
}

func TestE2EWorkerFailureRetryKeepsFreeReceipt(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, entitlements.New(pg, rd), ai.New(pg, rd), nil)
	uid := testutil.NewUser(t, ctx, pg)
	key := "free-worker-retry"
	req := httptest.NewRequest(http.MethodPost, "/v1/readings", nil)
	rec := httptest.NewRecorder()
	id, _, _, ok := svc.prepareReading(rec, req, uid, key, createRequest{SpreadCode: "daily", Question: "retry"})
	if !ok || id == "" {
		t.Fatalf("first prepare: id=%q ok=%v body=%s", id, ok, rec.Body.String())
	}
	if _, err := pg.Exec(ctx, `UPDATE readings SET status='failed' WHERE id=$1 AND quota_state='allowed'`, id); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	id2, _, _, ok := svc.prepareReading(rec, req, uid, key, createRequest{SpreadCode: "daily", Question: "retry"})
	if !ok || id2 != id {
		t.Fatalf("retry id=%q ok=%v body=%s", id2, ok, rec.Body.String())
	}
	var used, receipts int
	if err := pg.QueryRow(ctx, `SELECT free_used_today FROM entitlements WHERE user_id=$1`, uid).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM reading_authorization_receipts WHERE reading_id=$1`, id).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if used != 1 || receipts != 1 {
		t.Fatalf("used=%d receipts=%d", used, receipts)
	}
}

func TestE2EAuthorizeReadingErrorMarksUncheckedFailed(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, entitlements.New(pg, rd), ai.New(pg, rd), nil)
	uid := testutil.NewUser(t, ctx, pg)
	var paymentID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status)
		SELECT $1, id, 'single_99', 99, 'tg_stars', 'authorize-error:' || gen_random_uuid(), 99, 66, 'succeeded'
		  FROM plans WHERE code='single_99' ORDER BY valid_from DESC LIMIT 1
		RETURNING id`, uid).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	var singleID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO single_entitlements (user_id, spread_code, payment_id)
		VALUES ($1,'decision',$2) RETURNING id::text`, uid, paymentID).Scan(&singleID); err != nil {
		t.Fatal(err)
	}
	blocker, err := pg.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	var lockedID string
	if err := blocker.QueryRow(ctx, `SELECT id::text FROM single_entitlements WHERE id=$1 FOR UPDATE`, singleID).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/readings", nil)
	rec := httptest.NewRecorder()
	_, _, _, ok := svc.prepareReading(rec, req, uid, "authorize-error", createRequest{SpreadCode: "decision", Question: "retry"})
	if ok || rec.Code != http.StatusInternalServerError {
		t.Fatalf("authorize error ok=%v code=%d body=%s", ok, rec.Code, rec.Body.String())
	}
	var readingID, status, quota string
	if err := pg.QueryRow(ctx, `
		SELECT id::text, status, quota_state FROM readings
		 WHERE user_id=$1 AND idempotency_key='authorize-error'`, uid).Scan(&readingID, &status, &quota); err != nil {
		t.Fatal(err)
	}
	if readingID == "" || status != "failed" || quota != "error" {
		t.Fatalf("reading=%s status=%s quota=%s", readingID, status, quota)
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestE2EPendingUncheckedReadingDoesNotReturnAccepted(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, entitlements.New(pg, rd), ai.New(pg, rd), nil)
	uid := testutil.NewUser(t, ctx, pg)
	cardsJSON, _ := json.Marshal(draw(31, 1))
	var readingID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, interpretation, seed, status, idempotency_key, quota_state, worker_lease_until)
		VALUES ($1,'daily','unchecked',$2,'',1,'pending','unchecked-response','unchecked',now()+interval '45 seconds')
		RETURNING id`, uid, cardsJSON).Scan(&readingID); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/readings", nil)
	rec := httptest.NewRecorder()
	svc.respondReading(rec, req, uid, readingID)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestE2EAiInputsRejectsIncompleteContext(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, entitlements.New(pg, rd), ai.New(pg, rd), nil)
	cards := draw(11, 1)
	if _, _, _, complete := svc.aiInputsChecked(ctx, "daily", cards); !complete {
		t.Fatal("complete context rejected")
	}
	cards[0].Position = 1
	if _, _, _, complete := svc.aiInputsChecked(ctx, "daily", cards); complete {
		t.Fatal("incomplete position context accepted")
	}
}

func TestE2ECrisisObfuscationDoesNotConsume(t *testing.T) {
	do, _ := testClient(t)
	rec := do("POST", "/v1/readings", `{"spread_code":"daily","question":"п о к о н ч и т ь с собой"}`,
		map[string]string{"Idempotency-Key": "cr-obfuscated"})
	if rec.Code != 200 {
		t.Fatalf("crisis create: %d %s", rec.Code, rec.Body.String())
	}
	rec2 := do("POST", "/v1/readings", `{"spread_code":"daily"}`,
		map[string]string{"Idempotency-Key": "cr-obfuscated-next"})
	if rec2.Code != 200 {
		t.Fatalf("crisis consumed limit: %d %s", rec2.Code, rec2.Body.String())
	}
}

func TestE2ETerminalRetryRejectsChangedQuestion(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, entitlements.New(pg, rd), ai.New(pg, rd), nil)
	uid := testutil.NewUser(t, ctx, pg)
	cardsJSON, _ := json.Marshal(draw(41, 1))
	var readingID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, interpretation, seed, status, idempotency_key, quota_state)
		VALUES ($1,'daily','original',$2,'done',1,'done','question-conflict','allowed')
		RETURNING id`, uid, cardsJSON).Scan(&readingID); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/readings", nil)
	rec := httptest.NewRecorder()
	_, _, _, ok := svc.prepareReading(rec, req, uid, "question-conflict", createRequest{SpreadCode: "daily", Question: "changed"})
	if ok || rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("ok=%v code=%d body=%s", ok, rec.Code, rec.Body.String())
	}
	var question string
	if err := pg.QueryRow(ctx, `SELECT question FROM readings WHERE id=$1`, readingID).Scan(&question); err != nil {
		t.Fatal(err)
	}
	if question != "original" {
		t.Fatalf("stored question=%q", question)
	}
}

func TestE2ETerminalQuotaGuardRejectsMixedWorkerUpdate(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	_ = rd
	uid := testutil.NewUser(t, ctx, pg)
	cardsJSON, _ := json.Marshal(draw(43, 1))
	if _, err := pg.Exec(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, interpretation, seed, status, quota_state)
		VALUES ($1,'daily','insert guard',$2,'insert secret',1,'pending','unchecked')`, uid, cardsJSON); err == nil {
		t.Fatal("non-allowed direct insert was accepted")
	}
	var readingID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, seed, status, quota_state)
		VALUES ($1,'daily','guard',$2,1,'pending','unchecked')
		RETURNING id`, uid, cardsJSON).Scan(&readingID); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `UPDATE readings SET status='done', interpretation='mixed worker' WHERE id=$1`, readingID); err == nil {
		t.Fatal("unchecked reading was finalized")
	}
	var status, quota string
	if err := pg.QueryRow(ctx, `SELECT status, quota_state FROM readings WHERE id=$1`, readingID).Scan(&status, &quota); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || quota != "unchecked" {
		t.Fatalf("status=%s quota=%s", status, quota)
	}
}

func TestE2EChangedSpreadRetryUsesStoredCards(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, entitlements.New(pg, rd), ai.New(pg, rd), nil)
	uid := testutil.NewUser(t, ctx, pg)
	var oldPositions json.RawMessage
	var oldActive bool
	if err := pg.QueryRow(ctx, `SELECT positions, is_active FROM spreads WHERE code='decision'`).Scan(&oldPositions, &oldActive); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `UPDATE spreads SET positions=$1, is_active=$2 WHERE code='decision'`, oldPositions, oldActive)
	})
	cards := draw(67, 3)
	cardsJSON, _ := json.Marshal(cards)
	key := "changed-spread-retry"
	var readingID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, interpretation, seed, status, idempotency_key, quota_state)
		VALUES ($1,'decision','changed spread',$2,'',1,'failed',$3,'error')
		RETURNING id`, uid, cardsJSON, key).Scan(&readingID); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `
		INSERT INTO reading_authorization_receipts (reading_id, user_id, kind)
		VALUES ($1,$2,'legacy')`, readingID, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `UPDATE spreads SET positions='[{"label":"changed","meaning":"changed"}]'::jsonb, is_active=false WHERE code='decision'`); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/readings", nil)
	rec := httptest.NewRecorder()
	id, verdict, retriedCards, ok := svc.prepareReading(rec, req, uid, key, createRequest{SpreadCode: "decision", Question: "changed spread"})
	if !ok || id != readingID || !verdict.Allow || len(retriedCards) != len(cards) {
		t.Fatalf("retry id=%q verdict=%+v cards=%d ok=%v body=%s", id, verdict, len(retriedCards), ok, rec.Body.String())
	}
	var status, quota string
	if err := pg.QueryRow(ctx, `SELECT status, quota_state FROM readings WHERE id=$1`, readingID).Scan(&status, &quota); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || quota != "allowed" {
		t.Fatalf("status=%s quota=%s", status, quota)
	}
}

func TestE2ESameKeyConcurrentRequestsGenerateOnce(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"Барьер "}}]}`)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"один."}}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer srv.Close()
	t.Setenv("OPENROUTER_API_KEY", "test-key-0123456789abcdef")
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")

	ctx, pg, rd := testutil.Live(t)
	au := auth.New(pg, rd)
	uid := testutil.NewUser(t, ctx, pg)
	tok, err := auth.IssueJWT(uid, auth.UserTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err := rd.Set(ctx, "sess:"+uid, "x", auth.UserTTL).Err(); err != nil {
		t.Fatal(err)
	}
	svc := New(pg, entitlements.New(pg, rd), ai.New(pg, rd), nil)
	r := chi.NewRouter()
	r.With(au.RequireAuth).Post("/v1/readings", svc.HandleCreate)

	const workers = 4
	key := fmt.Sprintf("concurrent-%d", time.Now().UnixNano())
	start := make(chan struct{})
	codes := make([]int, workers)
	ids := make([]string, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodPost, "/v1/readings",
				strings.NewReader(`{"spread_code":"daily","question":"барьер"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", key)
			req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			codes[i] = rec.Code
			var body map[string]any
			if json.Unmarshal(rec.Body.Bytes(), &body) == nil {
				if value, ok := body["reading_id"].(string); ok {
					ids[i] = value
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()

	var storedID string
	var readings, receipts, used int
	var status, interpretation string
	if err := pg.QueryRow(ctx, `
		SELECT id::text, status, interpretation FROM readings
		 WHERE user_id=$1 AND idempotency_key=$2`, uid, key).Scan(&storedID, &status, &interpretation); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM readings WHERE user_id=$1 AND idempotency_key=$2`, uid, key).Scan(&readings); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM reading_authorization_receipts WHERE reading_id=$1`, storedID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `SELECT free_used_today FROM entitlements WHERE user_id=$1`, uid).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if readings != 1 || receipts != 1 || used != 1 || calls.Load() != 1 {
		t.Fatalf("readings=%d receipts=%d used=%d provider_calls=%d", readings, receipts, used, calls.Load())
	}
	if status != "done" || interpretation == "" {
		t.Fatalf("status=%s interpretation=%q", status, interpretation)
	}
	for i := 0; i < workers; i++ {
		if codes[i] != http.StatusOK && codes[i] != http.StatusAccepted {
			t.Fatalf("worker %d: code=%d body=%s", i, codes[i], ids[i])
		}
		if ids[i] != storedID {
			t.Fatalf("worker %d: reading_id=%q want=%q", i, ids[i], storedID)
		}
	}
}

func TestE2EGenerateInactiveSpreadPersistsTerminalFallback(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	var active bool
	if err := pg.QueryRow(ctx, `SELECT is_active FROM spreads WHERE code='newyear'`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `UPDATE spreads SET is_active=$1 WHERE code='newyear'`, active)
	})
	if _, err := pg.Exec(ctx, `UPDATE spreads SET is_active=false WHERE code='newyear'`); err != nil {
		t.Fatal(err)
	}
	svc := New(pg, entitlements.New(pg, rd), ai.New(pg, rd), nil)
	uid := testutil.NewUser(t, ctx, pg)
	cards := draw(77, 5)
	cardsJSON, _ := json.Marshal(cards)
	var id string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, interpretation, seed, status, quota_state, worker_lease_until)
		VALUES ($1,'newyear','',$2,'',1,'pending','allowed',now()+interval '2 minutes') RETURNING id`, uid, cardsJSON).Scan(&id); err != nil {
		t.Fatal(err)
	}
	svc.generate(ctx, id, "newyear", "", cards)
	var status, interpretation string
	if err := pg.QueryRow(ctx, `SELECT status, interpretation FROM readings WHERE id=$1`, id).Scan(&status, &interpretation); err != nil {
		t.Fatal(err)
	}
	if status != "done" || interpretation == "" {
		t.Fatalf("inactive spread left status=%s interpretation=%q", status, interpretation)
	}
}

func TestE2ERefundedSingleEntitlementReplayIsPaywall(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, entitlements.New(pg, rd), ai.New(pg, rd), nil)
	uid := testutil.NewUser(t, ctx, pg)
	var paymentID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status)
		SELECT $1, id, 'single_99', 99, 'tg_stars', 'refund:' || gen_random_uuid(), 99, 66, 'succeeded'
		  FROM plans WHERE code='single_99' ORDER BY valid_from DESC LIMIT 1
		RETURNING id`, uid).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	var entitlementID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO single_entitlements (user_id, spread_code, payment_id)
		VALUES ($1,'decision',$2) RETURNING id::text`, uid, paymentID).Scan(&entitlementID); err != nil {
		t.Fatal(err)
	}
	key := "refunded-single"
	req := httptest.NewRequest(http.MethodPost, "/v1/readings", nil)
	rec := httptest.NewRecorder()
	id, verdict, _, ok := svc.prepareReading(rec, req, uid, key, createRequest{SpreadCode: "decision", Question: "refund"})
	if !ok || id == "" || !verdict.Allow || verdict.Reason != "single" {
		t.Fatalf("first prepare: id=%q verdict=%+v ok=%v body=%s", id, verdict, ok, rec.Body.String())
	}
	if _, err := pg.Exec(ctx, `DELETE FROM single_entitlements WHERE id=$1`, entitlementID); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `UPDATE readings SET status='failed' WHERE id=$1 AND quota_state='allowed'`, id); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		rec = httptest.NewRecorder()
		_, verdict, _, ok = svc.prepareReading(rec, req, uid, key, createRequest{SpreadCode: "decision", Question: "refund"})
		if ok || verdict.Allow || rec.Code != http.StatusPaymentRequired {
			t.Fatalf("attempt %d: ok=%v verdict=%+v code=%d body=%s", attempt, ok, verdict, rec.Code, rec.Body.String())
		}
	}
	var total int
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM single_entitlements WHERE user_id=$1`, uid).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatalf("refund left %d entitlements", total)
	}
}

func TestE2EPendingUncheckedReplayDoesNotGenerate(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"replay"}}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer srv.Close()
	t.Setenv("OPENROUTER_API_KEY", "test-key-0123456789abcdef")
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")

	ctx, pg, rd := testutil.Live(t)
	au := auth.New(pg, rd)
	uid := testutil.NewUser(t, ctx, pg)
	tok, err := auth.IssueJWT(uid, auth.UserTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err := rd.Set(ctx, "sess:"+uid, "x", auth.UserTTL).Err(); err != nil {
		t.Fatal(err)
	}
	svc := New(pg, entitlements.New(pg, rd), ai.New(pg, rd), nil)
	r := chi.NewRouter()
	r.With(au.RequireAuth).Post("/v1/readings", svc.HandleCreate)

	key := fmt.Sprintf("pending-replay-%d", time.Now().UnixNano())
	cardsJSON, _ := json.Marshal(draw(53, 1))
	var storedID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, interpretation, seed, status, idempotency_key, quota_state, worker_lease_until)
		VALUES ($1,'daily','unchecked replay',$2,'',1,'pending',$3,'unchecked',now()+interval '2 minutes')
		RETURNING id`, uid, cardsJSON, key).Scan(&storedID); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/readings",
		strings.NewReader(`{"spread_code":"daily","question":"unchecked replay"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("replay body: %s", rec.Body.String())
	}
	if rec.Code != http.StatusAccepted || body["reading_id"] != storedID {
		t.Fatalf("replay code=%d body=%s", rec.Code, rec.Body.String())
	}
	var status, quota, interpretation string
	var readings, receipts, used int
	if err := pg.QueryRow(ctx, `SELECT status, quota_state, interpretation FROM readings WHERE id=$1`, storedID).
		Scan(&status, &quota, &interpretation); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM readings WHERE user_id=$1 AND idempotency_key=$2`, uid, key).Scan(&readings); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM reading_authorization_receipts WHERE reading_id=$1`, storedID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `SELECT free_used_today FROM entitlements WHERE user_id=$1`, uid).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || readings != 1 || receipts != 1 || used != 1 || interpretation != "" {
		t.Fatalf("provider_calls=%d readings=%d receipts=%d used=%d interpretation=%q", calls.Load(), readings, receipts, used, interpretation)
	}
	if status != "pending" || quota != "allowed" {
		t.Fatalf("status=%s quota=%s", status, quota)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
