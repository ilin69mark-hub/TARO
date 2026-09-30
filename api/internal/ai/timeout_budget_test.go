// A13/F-11: бюджет TTFT и бюджет генерации — разные вещи.
//
// Находка: один и тот же 8-секундный таймаут стоял и как ResponseHeaderTimeout,
// и как общий дедлайн попытки. Медленный, но исправный провайдер (healthy-поток
// длиннее 8 с) обрывался на 8-й секунде, попытка засчитывалась как провал
// провайдера, breaker открывался на ЗАДЕРЖКУ, и все последующие запросы шли
// по fallback-пути.
//
// Здесь два mock-провайдера:
//   - healthy, но медленный (12 с) — обязан завершиться успешно и НЕ тронуть
//     breaker;
//   - 5xx — обязан по-прежнему открывать breaker.
package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestSlowHealthyStreamCompletes — 12-секундный healthy-поток должен успеть.
// Спиннер задерживает первый токен на 1 с (TTFT в норме) и тянет остальные
// 11 с (генерация длиннее старого общего таймаута).
func TestSlowHealthyStreamCompletes(t *testing.T) {
	ctx, _, rd, gw := testGateway(t)
	// Своя модель через тестовый шов: breaker-счётчик гарантированно наш, его не
	// тронет ни один соседний пакет, идущий параллельно по общей БД/Redis.
	const model = "test/slow-healthy"
	gw.cfgOverride = &Config{Model: model, MaxTokens: 32, Temperature: 0, MonthlyCalls: 1000000}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		// TTFT: первая секунда тишины — это нормально, 8 с на неё есть.
		time.Sleep(1 * time.Second)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"Расклад "}}]}`)
		w.(http.Flusher).Flush()
		// Генерация: 11 секунд равномерного потока — дольше старого 8-секундного
		// общего дедлайма, который и ронялhealthy-потоки.
		for i := 0; i < 11; i++ {
			time.Sleep(1 * time.Second)
			fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"говорит"}}]}`)
			w.(http.Flusher).Flush()
		}
		fmt.Fprintln(w, `data: [DONE]`)
		w.(http.Flusher).Flush()
	}))
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")

	// Пакеты go test ./... идут ПАРАЛЛЕЛЬНО и делят одну БД/Redis, поэтому
	// абсолютное значение breaker-счётчика может изменить чужой тест. Проверяем
	// именно ДЕЛЬТУ своей попытки: успешный медленный поток не должен добавить
	// в счётчик ни единицы.
	if fails := breakerFails(t, ctx, rd, model); fails != 0 {
		t.Fatalf("precondition: breaker counter must start at 0, got %d", fails)
	}

	// completeReading сам пишет в ai_logs и возвращает только текст: успешный
	// результат = непустой текст вместо SafeReplacement-заглушки.
	text := gw.completeReading(ctx, "", "daily", testPos, testCards, "")
	if text == "" || text == SafeReplacement {
		t.Fatalf("healthy 12s stream must complete, got %q", text)
	}

	// Breaker не должен открыться на медленной, но успешной генерации.
	if fails, open := breakerState(t, ctx, rd, model); fails != 0 || open {
		t.Fatalf("healthy slow stream must not touch the breaker: fails=%d open=%v", fails, open)
	}
}

// TestProviderErrorOpensBreaker — контр-случай: настоящая ошибка провайдера
// (5xx) обязана по-прежнему открывать breaker. Иначе «лечение» F-11 просто
// выключило бы защиту.
func TestProviderErrorOpensBreaker(t *testing.T) {
	ctx, _, rd, gw := testGateway(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")

	// Вызываем столько раз, сколько нужно, чтобы копитель набрал порог.
	for i := 0; i < BreakerThreshold; i++ {
		ch := make(chan string, 64)
		_, _, _, err := gw.Stream(ctx, "", "daily", testPos, testCards, "", ch)
		drain(ch)
		if err == nil {
			t.Fatal("5xx provider must produce an error")
		}
		if open, _ := rd.Exists(ctx, breakerKey(gwModel(t, ctx, gw))).Result(); open > 0 {
			return // breaker открылся — поведение сохранено
		}
	}
	t.Fatalf("breaker must open after %d provider 5xx, но не открылся", BreakerThreshold)
}

// TestClientCancelDoesNotOpenBreaker — отмена клиентом (или наш общий дедлайн
// генерации) не должна трактоваться как поломка провайдера.
func TestClientCancelDoesNotOpenBreaker(t *testing.T) {
	ctx, _, rd, gw := testGateway(t)

	hold := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text-event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-hold // провайдер «думает», не отвечая
	}))
	defer srv.Close()
	defer close(hold)
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")

	cancelCtx, cancel := context.WithCancel(ctx)
	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()
	ch := make(chan string, 64)
	if _, _, _, err := gw.Stream(cancelCtx, "", "daily", testPos, testCards, "", ch); err == nil {
		drain(ch)
		t.Fatal("cancelled request must return an error")
	}
	drain(ch)
	if fails, err := rd.Get(ctx, breakerKey(gwModel(t, ctx, gw))+":fails").Int64(); err == nil && fails != 0 {
		t.Fatalf("client cancellation must not count as a provider failure, got %d", fails)
	}
	if open, _ := rd.Exists(ctx, breakerKey(gwModel(t, ctx, gw))).Result(); open > 0 {
		t.Fatal("client cancellation must not open the breaker")
	}
}

// TestGenerationDeadlineDoesNotOpenBreaker — наш СОБСТВЕННЫЙ дедлайн генерации
// (тот самый, что раньше был 8-секундным общим таймаутом) не должен
// засчитываться провайдеру: провайдер отдал заголовки и начал поток, мы просто
// не дождались. Иначе breaker открывается на задержке, а не на отказе, и всё
// уходит в fallback.
func TestGenerationDeadlineDoesNotOpenBreaker(t *testing.T) {
	ctx, _, rd, gw := testGateway(t)
	t.Setenv("AI_GENERATION_TIMEOUT", "1s") // дедлайн генерации вместо 60 с

	// Порядок defer'ов важен: LIFO — сначала отпускаем хендлер, потом закрываем
	// сервер. Иначе srv.Close() ждёт завершения запроса, который висит на hold.
	hold := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		// Заголовки и первый токен — сразу: TTFT в норме, провайдер исправен.
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"начало"}}]}`)
		w.(http.Flusher).Flush()
		<-hold // дальше провайдер молчит: это наш таймаут, а не его поломка
	}))
	defer srv.Close()
	defer close(hold)
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")

	ch := make(chan string, 64)
	_, _, _, err := gw.Stream(ctx, "", "daily", testPos, testCards, "", ch)
	drain(ch)
	if err == nil {
		t.Fatal("attempt cut by our generation deadline must return an error")
	}
	for _, model := range []string{gwModel(t, ctx, gw)} {
		if fails, err := rd.Get(ctx, breakerKey(model)+":fails").Int64(); err == nil && fails != 0 {
			t.Fatalf("our own generation deadline must not count as provider failure for %s, got %d", model, fails)
		}
		if open, _ := rd.Exists(ctx, breakerKey(model)).Result(); open > 0 {
			t.Fatalf("our own generation deadline must not open the breaker for %s", model)
		}
	}
}

// TestTTFTBudgetIsSeparateAndCountsAsFailure — вторая половина разделённых
// бюджетов: провайдер, который вообще не прислал заголовки, обрывается по
// TTFT-бюджету (короткому) и это ЧЕСТНЫЙ провайдерский провал, он идёт в
// breaker. Если бы TTFT-бюджет тоже стал длинным, «молчащий провайдер»
// висел бы на общем дедлайке генерации.
func TestTTFTBudgetIsSeparateAndCountsAsFailure(t *testing.T) {
	t.Setenv("AI_TTFT_TIMEOUT", "1s") // TTFT-бюджет ставим ДО сборки gateway
	ctx, _, rd, gw := testGateway(t)

	hold := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-hold // заголовков не будет никогда
	}))
	defer srv.Close()
	defer close(hold)
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")

	start := time.Now()
	ch := make(chan string, 64)
	_, _, _, err := gw.Stream(ctx, "", "daily", testPos, testCards, "", ch)
	drain(ch)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("provider without headers must fail")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("TTFT budget must cut the attempt quickly, took %s", elapsed)
	}
	// Провайдер нездоров: его таймаут — настоящая поломка, breaker обязан её видеть.
	if fails, err := rd.Get(ctx, breakerKey(gwModel(t, ctx, gw))+":fails").Int64(); err != nil || fails == 0 {
		t.Fatalf("TTFT timeout must count as a provider failure, fails=%d err=%v", fails, err)
	}
}

// TestBreakerClassification — таблица решений breaker'а. Интеграционные тесты
// не могут отличить «отмена клиента» от «наш таймаут» на стороне HTTP, поэтому
// саму классификацию фиксируем здесь: что считается поломкой провайдера, а что
// — нашей собственной задержкой.
func TestBreakerClassification(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		budgetGone bool
		wantCount  bool // уходит в breaker-счётчик
	}{
		{"provider 5xx", fmt.Errorf("provider 500"), false, true},
		{"incomplete stream", ErrIncompleteStream, false, true},
		// Настоящий сетевой таймаут транспорта (os.ErrDeadlineExceeded реализует
		// net.Error) — провайдер не прислал заголовки, это его поломка.
		{"ttft timeout (budget alive)", fmt.Errorf("Post: %w", os.ErrDeadlineExceeded), false, true},
		{"our generation deadline", context.DeadlineExceeded, true, false},
		{"client cancel", context.Canceled, false, false},
		{"our budget + canceled", context.Canceled, true, false},
		{"nothing", nil, false, false},
	}
	for _, tc := range cases {
		got := isServerError(tc.err) && !ourBudgetExpired(tc.budgetGone, tc.err)
		if got != tc.wantCount {
			t.Errorf("%s: counted=%v, want %v", tc.name, got, tc.wantCount)
		}
	}
}

func breakerState(t *testing.T, ctx context.Context, rd *redis.Client, model string) (int64, bool) {
	t.Helper()
	open, _ := rd.Exists(ctx, breakerKey(model)).Result()
	return breakerFails(t, ctx, rd, model), open > 0
}

func breakerFails(t *testing.T, ctx context.Context, rd *redis.Client, model string) int64 {
	t.Helper()
	n, err := rd.Get(ctx, breakerKey(model)+":fails").Int64()
	if err != nil {
		return 0
	}
	return n
}

// gwModel — модель, с которой реально работает гейтвей. В тестах она уникальна на
// прогон (изоляция пакета), поэтому breaker-счётчики заведомо начинают с нуля.
func gwModel(t *testing.T, ctx context.Context, gw *Gateway) string {
	t.Helper()
	return gw.LoadConfig(ctx).Model
}
