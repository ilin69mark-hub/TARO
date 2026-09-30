// A14/F-18.6: контракт SSE с обеих сторон.
//
// Раньше SSE проверялся только `strings.Contains(body, "data:")` — этого
// достаточно, чтобы пропустить смену формы кадра (например, `"text"` вместо
// `"token"`), и веб-клиент при этом молча получал пустой расклад: он парсит
// `data: {json}` и смотрит в поля `token` и `done`.
//
// Здесь контракт зафиксирован дважды:
//   - здесь: ответ разбирается ТЕМ ЖЕ парсером, что в web/lib/api.ts
//     (postReadingSSE) — префикс `data:`, разделитель кадров пустая строка,
//     поля token/done/reading_id/status;
//   - в web/lib/api.sse.test.ts: клиент кормит настоящими кадрами отсюда.
package readings

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sseFrame — ровно то, что ждёт web-клиент.
type sseFrame struct {
	Token     string `json:"token,omitempty"`
	Done      bool   `json:"done,omitempty"`
	ReadingID string `json:"reading_id,omitempty"`
	Status    string `json:"status,omitempty"`
	Fallback  bool   `json:"fallback,omitempty"`
}

// parseSSELikeWebClient повторяет разбор из web/lib/api.ts: режем по "\n\n",
// берём строки, начинающиеся с "data:", JSON.parse после "data:".
func parseSSELikeWebClient(t *testing.T, body string) (tokens []string, last sseFrame, fallback bool) {
	t.Helper()
	for _, raw := range strings.Split(body, "\n\n") {
		frame := strings.TrimSpace(raw)
		if frame == "" {
			continue
		}
		if !strings.HasPrefix(frame, "data:") {
			t.Fatalf("SSE frame must start with 'data:', got %q", frame)
		}
		payload := strings.TrimSpace(strings.TrimPrefix(frame, "data:"))
		var f sseFrame
		if err := json.Unmarshal([]byte(payload), &f); err != nil {
			t.Fatalf("SSE payload must be JSON, got %q (%v)", payload, err)
		}
		if f.Token != "" {
			tokens = append(tokens, f.Token)
		}
		if f.Fallback {
			fallback = true
		}
		if f.Done {
			last = f
		}
	}
	return tokens, last, fallback
}

// TestSSEContractMatchesWebClient — живой SSE обязан разбираться тем же
// парсером, что и на фронте, и нести те же поля.
func TestSSEContractMatchesWebClient(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key-0123456789abcdef")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"Карты "}}]}`)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"говорят."}}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")

	do, _ := testClient(t)
	rec := do("POST", "/v1/readings", `{"spread_code":"daily","question":"`+uniqQuestion(t)+`"}`,
		map[string]string{"Idempotency-Key": "sse-contract-" + uniqQuestion(t), "Accept": "text/event-stream"})
	if rec.Code != 200 {
		t.Fatalf("SSE: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type must be text/event-stream, got %q", ct)
	}
	body := rec.Body.String()
	if !strings.HasSuffix(body, "\n\n") {
		t.Fatalf("SSE stream must end with a blank line, got %q", body[max(0, len(body)-20):])
	}
	tokens, last, fallback := parseSSELikeWebClient(t, body)
	if len(tokens) == 0 {
		t.Fatalf("client would render nothing: no token frames in %q", body)
	}
	if joined := strings.Join(tokens, ""); joined == "" {
		t.Fatal("tokens must concatenate into text")
	}
	// Финальный кадр обязан нести reading_id+status: клиент по ним сохраняет
	// расклад, и без них reading_id был бы пустым.
	if fallback {
		t.Fatalf("live stream must not be marked as fallback: %+v", last)
	}
	if !last.Done {
		t.Fatalf("stream must end with done frame, got %+v", last)
	}
	if last.ReadingID == "" || last.Status == "" {
		t.Fatalf("done frame must carry reading_id and status, got %+v", last)
	}
}

// TestSSEFrameShapesParseLikeWebClient — контракт на уровне самих кадров: обе
// формы, которые сервер вообще шлёт (живая генерация и помеченный fallback),
// разбираются парсером клиента одинаково. Проверка формы, а не наличия:
// fallback-кадр сервер шлёт не во всех ветках (например, при выключенном
// gateway его нет), и требовать его всегда означало бы утверждать несуществующ
// гарантию.
func TestSSEFrameShapesParseLikeWebClient(t *testing.T) {
	live := "data: {\"token\":\"Карты \"}\n\n" +
		"data: {\"token\":\"говорят.\"}\n\n" +
		"data: {\"done\":true,\"reading_id\":\"11111111-2222-3333-4444-555555555555\",\"status\":\"done\"}\n\n"
	fallback := "data: {\"fallback\":true}\n\n" +
		"data: {\"done\":true,\"reading_id\":\"11111111-2222-3333-4444-666666666666\",\"status\":\"done\"}\n\n"

	tokens, last, liveFallback := parseSSELikeWebClient(t, live)
	if len(tokens) != 2 || tokens[0] != "Карты " || tokens[1] != "говорят." {
		t.Fatalf("live frames: tokens=%v", tokens)
	}
	if !last.Done || last.ReadingID == "" || last.Status != "done" {
		t.Fatalf("live final frame: %+v", last)
	}
	if liveFallback {
		t.Fatalf("live stream must not claim fallback: %+v", last)
	}

	ftokens, flast, fmarked := parseSSELikeWebClient(t, fallback)
	if len(ftokens) != 0 {
		t.Fatalf("fallback stream must not fake tokens, got %v", ftokens)
	}
	if !fmarked {
		t.Fatalf("fallback stream must be explicitly marked: %+v", flast)
	}
	if !flast.Done || flast.ReadingID == "" {
		t.Fatalf("fallback final frame: %+v", flast)
	}

	// Регрессия контракта: если сервер переименует поле token (например, в
	// "text"), клиент получит ноль токенов и отрисует пустоту. Здесь это
	// отлавливается на той же форме кадра.
	renamed := "data: {\"text\":\"Карты \"}\n\n" +
		"data: {\"done\":true,\"reading_id\":\"1\",\"status\":\"done\"}\n\n"
	rtokens, _, _ := parseSSELikeWebClient(t, renamed)
	if len(rtokens) != 0 {
		t.Fatalf("unknown field name must NOT look like a token, got %v (client would silently render text)", rtokens)
	}
}

// TestSSEFirstFrameCarriesReadingID — id обязан прийти ПЕРВЫМ кадром, до
// токенов толкования. Веб открывает страницу с картами по этому id, не дожидаясь
// генерации (владелец: «нажал → сразу переходить на карты»). Если id уедет в
// финальный кадр, пользователь будет смотреть на пустой экран все время
// генерации — а это до 25 секунд (таймаут streamLive).
func TestSSEFirstFrameCarriesReadingID(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-key-0123456789abcdef")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"Карты "}}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer srv.Close()
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)
	t.Setenv("OPENROUTER_ALLOW_CUSTOM_BASE", "1")

	do, _ := testClient(t)
	rec := do("POST", "/v1/readings", `{"spread_code":"daily","question":"`+uniqQuestion(t)+`"}`,
		map[string]string{"Idempotency-Key": "early-id-" + uniqQuestion(t), "Accept": "text/event-stream"})
	if rec.Code != 200 {
		t.Fatalf("SSE: %d %s", rec.Code, rec.Body.String())
	}

	// Первый непустой кадр.
	first := ""
	for _, raw := range strings.Split(rec.Body.String(), "\n\n") {
		frame := strings.TrimSpace(raw)
		if frame == "" {
			continue
		}
		first = strings.TrimSpace(strings.TrimPrefix(frame, "data:"))
		break
	}
	var f sseFrame
	if err := json.Unmarshal([]byte(first), &f); err != nil {
		t.Fatalf("first frame must be JSON, got %q (%v)", first, err)
	}
	if f.ReadingID == "" {
		t.Fatalf("first frame must carry reading_id (web navigates by it), got %q", first)
	}
	// Ранний кадр не должен выглядеть как завершение потока: иначе клиент
	// посчитает расклад готовым и не дождётся толкования.
	if f.Done {
		t.Fatalf("early frame must not be marked done: %q", first)
	}
	// ...и он обязан совпадать с id в финальном кадре: это один и тот же расклад.
	tokens, last, _ := parseSSELikeWebClient(t, rec.Body.String())
	if len(tokens) == 0 {
		t.Fatalf("live stream must still carry tokens: %q", rec.Body.String())
	}
	if !last.Done || last.ReadingID != f.ReadingID {
		t.Fatalf("early reading_id %q must match final %q (one reading, not two)", f.ReadingID, last.ReadingID)
	}
}

// TestSSEFirstFrameCarriesReadingID_NoGateway — то же для пути БЕЗ AI-шлюза
// (стенд и часть деплоев: нет OPENROUTER_API_KEY → синхронный fallback и
// streamReading вместо streamLive). Ранний кадр обязателен в обоих путях,
// иначе «сразу открыть карты» работает только у части пользователей.
//
// Ключ намеренно НЕ выставляем: тест обязан идти по streamReading.
func TestSSEFirstFrameCarriesReadingID_NoGateway(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("OPENROUTER_BASE_URL", "")

	do, _ := testClient(t)
	rec := do("POST", "/v1/readings", `{"spread_code":"daily","question":"`+uniqQuestion(t)+`"}`,
		map[string]string{"Idempotency-Key": "no-gw-early-" + uniqQuestion(t), "Accept": "text/event-stream"})
	if rec.Code != 200 {
		t.Fatalf("SSE: %d %s", rec.Code, rec.Body.String())
	}

	first := ""
	for _, raw := range strings.Split(rec.Body.String(), "\n\n") {
		if frame := strings.TrimSpace(raw); frame != "" {
			first = strings.TrimSpace(strings.TrimPrefix(frame, "data:"))
			break
		}
	}
	var f sseFrame
	if err := json.Unmarshal([]byte(first), &f); err != nil {
		t.Fatalf("first frame must be JSON, got %q (%v)", first, err)
	}
	if f.ReadingID == "" {
		t.Fatalf("first frame must carry reading_id on the no-gateway path too, got %q", first)
	}
	if f.Done {
		t.Fatalf("early frame must not be marked done: %q", first)
	}
	tokens, last, _ := parseSSELikeWebClient(t, rec.Body.String())
	if len(tokens) == 0 {
		t.Fatalf("stream must still carry tokens: %q", rec.Body.String())
	}
	if !last.Done || last.ReadingID != f.ReadingID {
		t.Fatalf("early reading_id %q must match final %q", f.ReadingID, last.ReadingID)
	}
}
