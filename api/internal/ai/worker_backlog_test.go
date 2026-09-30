// A15/F-06: потолок воркера. До правки конфигурация была жёсткой: batch=1
// (workerBatchSize) и тик 30s (cmd/api/main.go), то есть верхняя граница
// пропускной способности 1 чтение / 30 с = 2.00 readings/min. На DoD-бэклоге
// в 100 pending-чтений это 50 минут ожидания пользователя.
//
// Тест намеренно меряет ПРОД-конфигурацию (тот же tick, что передаёт main.go), а
// не тестовую: иначе он доказал бы только скорость мок-провайдера.
package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	backlogSize = 100
	// backlogDoD — граница из карточки A15: бэклог из 100 pending-чтений
	// дренируется меньше чем за 2 минуты.
	backlogDoD = 2 * time.Minute
	// backlogFloor — минимально допустимая скорость: 100 / 2мин = 50/min.
	// Проверяется явно, чтобы падение было читаемым, а не «тест умер по таймауту».
	backlogFloor = 50.0
	// backlogProviderLatency имитирует реальную генерацию. Это ключ к честности
	// теста: мгновенный ответ провайдера маскировал бы потолок, потому что
	// последовательный воркер на «бесплатных» запросах успевает обслужить 100
	// чтений быстрее любой границы DoD.
	backlogProviderLatency = 300 * time.Millisecond
	// backlogParallelBound — 100 чтений по backlogProviderLatency каждое.
	// Последовательная обработка стоила бы 100×300мс = 30 с, параллельная по 10 —
	// около 3 с. Порог 12 с (30/2.5) ловит и batch=1, и последовательный цикл по
	// jobs, оставляя четырёхкратный запас на загруженную машину: первый вариант
	// (600 мс / порог 15 с) давал лишь 2.5x и один раз фейлился под нагрузкой
	// всего прогона.
	backlogParallelBound = 12 * time.Second
	// idlePickupBound — потолок F-06 в его пользовательской форме: сколько
	// ждать первого чтения, когда очередь была пустой. При тике 30 с (исходный
	// дефект) это 30 с ожидания; при тике 2 с — 2 с.
	idlePickupBound = 10 * time.Second
)

// TestWorkerDrainsBacklog — DoD A15: 100 pending-чтений дренируются быстрее
// двух минут, ни одно не обрабатывается дважды, обработка идёт параллельно.
//
// Мутации, которые тест обязан ловить:
//   - тик 30s + ожидание только тика (исходный F-06) → дренируется 2 чтения;
//   - batch обратно в 1 → maxInFlight == 1 (обработка последовательная);
//   - последовательный цикл по jobs при batch=10 → maxInFlight == 1.
func TestWorkerDrainsBacklog(t *testing.T) {
	ctx, pg, _, gw := testGateway(t)

	// Числа из DoD обязаны быть именно такими. Иначе «зелёный» прогон этого
	// теста означал бы лишь то, что дефолты кто-то тихо откатил к потолку, а
	// проверять тут нечего.
	if tick := workerTick(); tick > 5*time.Second {
		t.Fatalf("default worker tick=%s, want <= 5s (DoD: 100 pending drained < 2min)", tick)
	}
	if batch := loadWorkerPolicy().batchSize; batch < 5 {
		t.Fatalf("default worker batch=%d, want >= 5 (DoD: backlog drains in parallel)", batch)
	}

	var calls, inFlight, maxInFlight atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			old := maxInFlight.Load()
			if current <= old || maxInFlight.CompareAndSwap(old, current) {
				break
			}
		}
		calls.Add(1)
		// Имитация реальной генерации: мгновенный ответ скрыл бы потолок
		// планировщика (DoD меряет расписание, а не скорость провайдера).
		time.Sleep(backlogProviderLatency)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"backlog"}}]}`)
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

	// Уникальный вопрос на чтение: promptHash входит в ключ кэша, поэтому
	// одинаковые вопросы дали бы 99 cache-hit'ов вместо работы провайдера.
	ids := make([]string, 0, backlogSize)
	for i := 0; i < backlogSize; i++ {
		var id string
		if err := pg.QueryRow(ctx, `
			INSERT INTO readings (user_id, spread_code, question, cards, seed, status, quota_state)
			VALUES ($1,'daily',$2, '[{"card_id":1,"reversed":false,"position":0}]',1,'pending','allowed')
			RETURNING id`, uid, uniqueQuestion(t)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}

	workerCtx, cancelWorker := context.WithCancel(ctx)
	done := gw.startWorker(workerCtx, pg, workerTick())

	start := time.Now()
	deadline := start.Add(backlogDoD)
	drained := 0
	for {
		if err := pg.QueryRow(ctx,
			`SELECT count(*) FROM readings WHERE id=ANY($1) AND status='done'`, ids).Scan(&drained); err != nil {
			t.Fatal(err)
		}
		if drained == backlogSize || time.Now().After(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	elapsed := time.Since(start)
	cancelWorker()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not stop")
	}

	rate := 0.0
	if elapsed > 0 {
		rate = float64(drained) / elapsed.Minutes()
	}
	t.Logf("backlog %d: drained=%d/%d in %s → %.2f readings/min (calls=%d, maxInFlight=%d, tick=%s)",
		backlogSize, drained, backlogSize, elapsed.Round(time.Millisecond), rate,
		calls.Load(), maxInFlight.Load(), workerTick())

	if drained != backlogSize {
		t.Fatalf("backlog must drain fully: %d/%d in %s → %.2f readings/min, want >= %.2f",
			drained, backlogSize, elapsed.Round(time.Millisecond), rate, backlogFloor)
	}
	if elapsed >= backlogDoD {
		t.Fatalf("backlog drained too slowly: %s, want < %s", elapsed.Round(time.Millisecond), backlogDoD)
	}
	if rate < backlogFloor {
		t.Fatalf("throughput below DoD floor: %.2f readings/min, want >= %.2f", rate, backlogFloor)
	}
	// Граница параллельности: последовательная обработка 100 чтений по
	// backlogProviderLatency стоила бы 60 с. Это ловит возврат batch=1 и
	// возврат последовательного цикла по jobs — обе формы исходного потолка.
	if elapsed >= backlogParallelBound {
		t.Fatalf("backlog drained without parallelism: %s, want < %s (sequential would take ~%s)",
			elapsed.Round(time.Millisecond), backlogParallelBound,
			(backlogSize * backlogProviderLatency).Round(time.Second))
	}

	// Ни одно чтение не должно быть обработано дважды: КАЖДЫЙ захват
	// инкрементит worker_attempts, поэтому сумма попыток = числу чтений.
	var attemptsSum, doneCount, emptyInterp int
	if err := pg.QueryRow(ctx, `
		SELECT COALESCE(SUM(worker_attempts),0),
		       count(*) FILTER (WHERE status='done'),
		       count(*) FILTER (WHERE status='done' AND COALESCE(interpretation,'')='')
		  FROM readings WHERE id=ANY($1)`, ids).Scan(&attemptsSum, &doneCount, &emptyInterp); err != nil {
		t.Fatal(err)
	}
	if attemptsSum != backlogSize || doneCount != backlogSize {
		t.Fatalf("each reading must be claimed exactly once: attempts=%d done=%d, want %d/%d",
			attemptsSum, doneCount, backlogSize, backlogSize)
	}
	if emptyInterp != 0 {
		t.Fatalf("every drained reading must keep its interpretation, empty=%d", emptyInterp)
	}
	// Последовательная обработка по одному чтению за раз — исходная причина
	// потолка: batch в 10 строк, но цикл по ним был последовательным.
	if maxInFlight.Load() < 2 {
		t.Fatalf("worker must process claimed readings concurrently, max in-flight=%d", maxInFlight.Load())
	}
}

// TestWorkerPicksUpIdleReadingFast — вторая половина F-06 в пользовательской
// форме. Бэклог-тест не видит тик: при полной очереди воркер дренирует её без
// ожидания. А вот первое чтение после простоя ждёт ровно тик — при жёстких
// 30 с это 30 секунд ожидания пользователя, хотя очередь была пустой.
//
// Мутации: тик 2 с → 30 с (исходный дефект) → падает; тик 30 с → 1 с →
// проходит, то есть тест ловит именно регрессию, а не «любое значение».
func TestWorkerPicksUpIdleReadingFast(t *testing.T) {
	ctx, pg, _, gw := testGateway(t)
	tick := workerTick()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"idle"}}]}`)
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

	workerCtx, cancelWorker := context.WithCancel(ctx)
	done := gw.startWorker(workerCtx, pg, tick)
	defer func() {
		cancelWorker()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("worker did not stop")
		}
	}()

	// Ждём, пока воркер сделает первый (пустой) drain — иначе мы бы измеряли
	// не тик, а время до старта горутины.
	time.Sleep(50 * time.Millisecond)

	var id string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, seed, status, quota_state)
		VALUES ($1,'daily',$2, '[{"card_id":1,"reversed":false,"position":0}]',1,'pending','allowed')
		RETURNING id`, uid, uniqueQuestion(t)).Scan(&id); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	deadline := start.Add(idlePickupBound)
	status := "pending"
	for status == "pending" || status == "pending_fallback" {
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
		if err := pg.QueryRow(ctx, `SELECT status FROM readings WHERE id=$1`, id).Scan(&status); err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)
	if status == "pending" || status == "pending_fallback" {
		t.Fatalf("idle reading was not picked up within %s (tick=%s): status=%s",
			idlePickupBound, tick, status)
	}
	t.Logf("idle reading picked up in %s (tick=%s)", elapsed.Round(time.Millisecond), tick)
	if elapsed >= idlePickupBound {
		t.Fatalf("idle pickup too slow: %s, want < %s (tick=%s)", elapsed.Round(time.Millisecond), idlePickupBound, tick)
	}
}

// TestWorkerTickIsNotHardcodedInMain — статический guard по образцу
// TestRefundRouteIsWiredBehindRequireAdmin (A14/F-18): package main не
// импортируется, поэтому точку сборки проверяем чтением исходника. Потолок
// 2.00 readings/min жил именно в cmd/api/main.go:90 жёстким 30*time.Second.
func TestWorkerTickIsNotHardcodedInMain(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "cmd", "api", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "ai.WorkerTick()") {
		t.Fatal("cmd/api/main.go must take the worker tick from ai.WorkerTick(), not a literal")
	}
	if strings.Contains(text, "StartWorker(workerCtx, pg, 30*time.Second)") {
		t.Fatal("cmd/api/main.go still hardcodes the 30s worker tick that capped throughput at 2.00 readings/min")
	}
}
