// A19/F-48: история чтений перебирала толкования.
//
// Замер ДО на 50 строках (потолок limit) с толкованиями по ~160 КБ:
// PostgreSQL отдавал 8 100 000 байт ради ответа размером ~8 КБ. Запрос просил
// `left(interpretation, 131073)` символов, а список показывает 160.
//
// Замер ПОСЛЕ: 56 500 байт на те же строки — перебор уменьшен в 143 раза.
package readings

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"taro/api/internal/ai"
	"taro/api/internal/auth"
	"taro/api/internal/entitlements"
	"taro/api/internal/referral"
	"taro/api/internal/testutil"
)

// historyBulk — размер одного толкования в тесте: близко к потолку ai.MaxOutputBytes.
const historyBulkRunes = 2700

// TestHistoryQueryDoesNotOverfetch — главное утверждение карточки: база не
// отдаёт приложению десятки мегабайт ради превью в 160 символов.
//
// Мутации: вернуть ai.MaxOutputBytes+1 в запрос (перебор 143x) и вернуть
// обрезку по байтам `s[:160]` (рвётся UTF-8 на кириллице).
func TestHistoryQueryDoesNotOverfetch(t *testing.T) {
	proxy := startCountingProxy(t)
	t.Setenv("DATABASE_URL", proxy.url())
	ctx, pg, rd := testutil.Live(t)
	en := entitlements.New(pg, rd)
	gw := ai.New(pg, rd)
	svc := New(pg, en, gw, referral.New(pg, en))
	uid, ids := seedHistory(t, ctx, pg, 50)
	list := historySetup(t, ctx, pg, rd, svc, uid)

	// Сколько октетов просит база по формуле (справочно, для лога).
	formula := historyFetchedBytes(t, ctx, pg, uid, historyPreviewFetchRunes)
	legacyFormula := historyFetchedBytes(t, ctx, pg, uid, 131073)

	// ГЛАВНОЕ измерение: сколько байт PostgreScript реально ОТДАЛ по сети за
	// один вызов истории. Считает прокси, поэтому утверждение не зависит от
	// констант в коде: возврат перебора сломает его независимо от имён.
	proxy.reset()
	rec := list(50)
	wire := proxy.bytesReceived()
	t.Logf("history wire: %d bytes per call (formula %d vs legacy formula %d, over-fetch %dx); response %d bytes",
		wire, formula, legacyFormula, legacyFormula/max(formula, 1), rec.Body.Len())

	// Порог DoD: 50 строк по 160 символов превью плюс служебные байты протокола.
	const budget = 1024 * 1024
	if wire > budget {
		t.Fatalf("history over-fetches over the wire: %d bytes for 50 previews, budget %d", wire, budget)
	}
	var items []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("history response is not valid JSON: %v (%s)", err, rec.Body.String()[:200])
	}
	if len(items) != 50 {
		t.Fatalf("history returned %d items, want 50", len(items))
	}
	for _, item := range items {
		preview, _ := item["preview"].(string)
		if !utf8.ValidString(preview) {
			t.Fatalf("preview is not valid UTF-8: %q", preview[len(preview)-8:])
		}
		if got := utf8.RuneCountInString(preview); got != historyPreviewRunes {
			t.Fatalf("preview has %d runes, want exactly %d: %q", got, historyPreviewRunes, preview)
		}
	}
	if rec.Body.Len() > 128*1024 {
		t.Fatalf("history response is %d bytes: previews must stay small", rec.Body.Len())
	}
	t.Logf("history response: %d bytes for 50 previews, all valid UTF-8, %d runes each", rec.Body.Len(), historyPreviewRunes)
	_ = ids
}

// TestHistoryPreviewTruncationIsRuneSafe — отдельный контракт обрезки: кириллица
// по 2 байта, 160 символов = 320 байт, и `s[:160]` разрезал бы символ пополам.
func TestHistoryPreviewTruncationIsRuneSafe(t *testing.T) {
	long := strings.Repeat("Карты", 500) // 500 символов по 2 байта
	got := truncateRunes(long, historyPreviewRunes)
	if !utf8.ValidString(got) {
		t.Fatal("truncation produced invalid UTF-8")
	}
	if n := utf8.RuneCountInString(got); n != historyPreviewRunes {
		t.Fatalf("got %d runes, want %d", n, historyPreviewRunes)
	}
	if long[:historyPreviewRunes*2] != got {
		t.Fatalf("truncation must keep the first %d runes verbatim", historyPreviewRunes)
	}
	if s := truncateRunes("короче", historyPreviewRunes); s != "короче" {
		t.Fatalf("short strings must pass through unchanged, got %q", s)
	}
	if s := truncateRunes(long, 0); s != "" {
		t.Fatalf("limit 0 must return empty string, got %q", s)
	}
}

// TestHistoryPreviewFetchWindowIsBounded — сторож контракта: окно, которое
// просит база, обязано быть заметно меньше полного толкования.
func TestHistoryPreviewFetchWindowIsBounded(t *testing.T) {
	if historyPreviewFetchRunes <= historyPreviewRunes {
		t.Fatalf("fetch window %d must exceed the preview length %d (stop-word window + rune-safe cut)",
			historyPreviewFetchRunes, historyPreviewRunes)
	}
	if historyPreviewFetchRunes > 4096 {
		t.Fatalf("fetch window %d is too large: the point of A19/F-48 is not to over-fetch", historyPreviewFetchRunes)
	}
}

// historySetup собирает НАСТОЯЩУЮ проводку: HandleList за RequireAuth с живой
// cookie. Контекст с user_id ставит только auth (userCtxKey недоступен снаружи),
// поэтому идём через роутер — так же, как в readings_e2e_test.go.
// historySetup принимает uid ЯВНО: «самый свежий пользователь» — гонка при
// параллельном `go test ./...` (соседний тест успевает создать своего
// пользователя позже, и список молча приходит пустым).
func historySetup(t *testing.T, ctx context.Context, pg *pgxpool.Pool, rd *redis.Client, svc *Service, uid string) func(limit int) *httptest.ResponseRecorder {
	t.Helper()
	tok, err := auth.IssueJWT(uid, auth.UserTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err := rd.Set(ctx, "sess:"+uid, "x", auth.UserTTL).Err(); err != nil {
		t.Fatal(err)
	}
	au := auth.New(pg, rd)
	r := chi.NewRouter()
	r.With(au.RequireAuth).Get("/v1/readings", svc.HandleList)
	return func(limit int) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/v1/readings?limit="+strconv.Itoa(limit), nil)
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("history: want 200 got %d: %s", rec.Code, rec.Body.String())
		}
		return rec
	}
}

func seedHistory(t *testing.T, ctx context.Context, pg *pgxpool.Pool, rows int) (string, []string) {
	t.Helper()
	var uid string
	if err := pg.QueryRow(ctx, `INSERT INTO users (anon_uuid) VALUES (gen_random_uuid()) RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, uid) })
	ids := make([]string, 0, rows)
	for i := 1; i <= rows; i++ {
		var id string
		if err := pg.QueryRow(ctx, `
			INSERT INTO readings (user_id, spread_code, question, cards, seed, status, interpretation, quota_state)
			VALUES ($1,'daily',$2,'[{"card_id":1,"reversed":false,"position":0}]',$3,'done',$4,'allowed')
			RETURNING id`, uid, "вопрос "+strings.Repeat("я", i%17+1), i,
			strings.Repeat("Карты говорят, что путь ваш ясен. ", historyBulkRunes)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return uid, ids
}

// historyFetchedBytes — сколько байт PostgreSQL обязан отдать приложению за
// выражение left(interpretation, $1). Это и есть величина перебора: приложению
// нужны 160 символов, а база едет по сети с полным окном.
func historyFetchedBytes(t *testing.T, ctx context.Context, pg *pgxpool.Pool, uid string, fetchRunes int) int {
	t.Helper()
	var total int64
	if err := pg.QueryRow(ctx, `
		SELECT COALESCE(SUM(octet_length(COALESCE(left(interpretation, $2), ''))), 0)
		  FROM (SELECT interpretation FROM readings
		         WHERE user_id=$1 AND status NOT IN ('cancelled','failed')
		         ORDER BY created_at DESC LIMIT 50) t`, uid, fetchRunes).Scan(&total); err != nil {
		t.Fatal(err)
	}
	return int(total)
}

// countingProxy — TCP-прокси между тестом и PostgreSQL, считающий байты,
// которые сервер ОТДАЛ клиенту.
//
// Почему так, а не константа в коде: перебор — это факт передачи, а не
// намерение. Ни pgx.QueryTracer (не видит DataRow), ни EXPLAIN (показывает
// буферы, а не отданные значения) не дают ответа на вопрос «сколько байт
// приехало в приложение». Прокси отвечает на него буквально.
type countingProxy struct {
	listener net.Listener
	// target — исходный DATABASE_URL: подменяем ТОЛЬКО host, сохраняя
	// пользователя, пароль, имя БД и параметры sslmode.
	target   url.URL
	received atomic.Int64
}

func startCountingProxy(t *testing.T) *countingProxy {
	t.Helper()
	raw := os.Getenv("DATABASE_URL")
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("DATABASE_URL must be a URL: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &countingProxy{listener: listener, target: *parsed}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			go proxy.pipe(client, parsed.Host)
		}
	}()
	return proxy
}

func (p *countingProxy) pipe(client net.Conn, upstreamHost string) {
	defer func() { _ = client.Close() }()
	server, err := net.Dial("tcp", upstreamHost)
	if err != nil {
		return
	}
	defer func() { _ = server.Close() }()
	done := make(chan struct{}, 2)
	// Считаем только сервер → клиент: это и есть «переданные байты».
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := server.Read(buf)
			if n > 0 {
				p.received.Add(int64(n))
				if _, werr := client.Write(buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := client.Read(buf)
			if n > 0 {
				if _, werr := server.Write(buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	<-done
	_ = client.Close()
	_ = server.Close()
}

func (p *countingProxy) url() string {
	proxied := p.target
	proxied.Host = p.listener.Addr().String()
	return proxied.String()
}

// reset обнуляет счётчик перед измеряемым вызовом.
func (p *countingProxy) reset() { p.received.Store(0) }

func (p *countingProxy) bytesReceived() int64 { return p.received.Load() }
