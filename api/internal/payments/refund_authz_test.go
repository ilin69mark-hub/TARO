// A14/F-18: refund-тест обязан проверять РЕАЛЬНУЮ проводку, а не голый
// обработчик. Раньше HandleRefund монтировался bare, поэтому регрессия
// авторизации на денежном эндпоинте была невидима, а ветка отказа Telegram
// вообще не исполнялась: без TG_BOT_TOKEN срабатывал ранний
// `!tgTokenReady()`-return и тест «проходил», ничего не проверяя.
package payments

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"taro/api/internal/admin"
	"taro/api/internal/me"
	"taro/api/internal/testutil"
)

// tgMock — локальный мок Telegram API. Через него реально уходит вызов
// refundStarPayment, поэтому ветки «Telegram отказал» и «Telegram подтвердил»
// исполняются по-настоящему, а не через ранний return.
func tgMock(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.Contains(r.URL.Path, "/refundStarPayment") {
			t.Errorf("unexpected telegram method: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(func() {
		if calls == 0 {
			t.Errorf("telegram mock was never called: refund-failure path did not execute")
		}
		srv.Close()
	})
	t.Setenv("TG_BOT_TOKEN", "123456:test-token-for-mock")
	t.Setenv("TELEGRAM_API_BASE", srv.URL)
	t.Setenv("TELEGRAM_ALLOW_CUSTOM_BASE", "1")
	return srv
}

// refundRouter собирает проводку как в проде: HandleRefund ЗА RequireAdmin.
func refundRouter(t *testing.T, pg *pgxpool.Pool, rd *redis.Client) http.Handler {
	t.Helper()
	ad := admin.New(pg, rd)
	py := New(pg, me.New(pg, rd))
	r := chi.NewRouter()
	r.With(ad.RequireAdmin).Post("/v1/admin/refund", py.HandleRefund)
	return r
}

// TestE2ERefundRequiresAdmin — денежный эндпоинт обязан быть за RequireAdmin.
// Мутация (снять RequireAdmin в этой сборке роутера или в cmd/admin/main.go,
// см. TestRefundRouteIsWiredBehindRequireAdmin) роняет этот тест.
func TestE2ERefundRequiresAdmin(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	t.Setenv("ADMIN_API_TOKEN", "admin-token-0123456789abcdef")
	r := refundRouter(t, pg, rd)

	call := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/admin/refund", strings.NewReader(`{"payment_id":"00000000-0000-0000-0000-000000000000"}`))
		req.Header.Set("Content-Type", "application/json")
		// RequireAdmin по X-Admin-Token пускает только с loopback.
		req.RemoteAddr = "127.0.0.1:5555"
		if token != "" {
			req.Header.Set("X-Admin-Token", token)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	if rec := call(""); rec.Code != http.StatusForbidden {
		t.Fatalf("refund without admin token must be 403, got %d %s", rec.Code, rec.Body.String())
	}
	if rec := call("wrong-token"); rec.Code != http.StatusForbidden {
		t.Fatalf("refund with wrong admin token must be 403, got %d", rec.Code)
	}
	// С верным токеном запрос доходит до обработчика: 404 (платежа нет), а не 403.
	// Именно эта разница доказывает, что middleware реально стоит на маршруте.
	if rec := call("admin-token-0123456789abcdef"); rec.Code != http.StatusNotFound {
		t.Fatalf("refund with valid admin token must reach the handler (404), got %d %s", rec.Code, rec.Body.String())
	}
	_ = ctx
}

// TestE2ERefundTelegramFailureIsRecorded — Telegram отказал: деньги не вернулись,
// поэтому платёж обязан остаться в 'succeeded', а refund_state — уйти в
// 'unknown' с причиной, чтобы админ увидел платёж в reconcile.
func TestE2ERefundTelegramFailureIsRecorded(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	tgMock(t, 500, `{"ok":false,"description":"Internal Server Error"}`)
	t.Setenv("ADMIN_API_TOKEN", "admin-token-0123456789abcdef")
	r := refundRouter(t, pg, rd)

	uid := testutil.NewUser(t, ctx, pg)
	if _, err := pg.Exec(ctx, `UPDATE users SET tg_id=820001 WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	var pid string
	if err := pg.QueryRow(ctx, `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider,
		                      provider_payment_id, amount_rub, stars, status, idempotency_key)
		SELECT $1, id, 'month_299', 299, 'tg_stars', 'tg:ch_a14_fail', 299, 199, 'succeeded',
		       'a14-fail-' || gen_random_uuid()
		  FROM plans WHERE code='month_299' LIMIT 1 RETURNING id::text`, uid).Scan(&pid); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/v1/admin/refund", strings.NewReader(`{"payment_id":"`+pid+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Token", "admin-token-0123456789abcdef")
	req.RemoteAddr = "127.0.0.1:5555"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("failed telegram refund must answer 502, got %d %s", rec.Code, rec.Body.String())
	}
	var status string
	var refundState, lastError, reconReason *string
	if err := pg.QueryRow(ctx,
		`SELECT status, refund_state, refund_last_error, reconciliation_reason
		   FROM payments WHERE id=$1`, pid).
		Scan(&status, &refundState, &lastError, &reconReason); err != nil {
		t.Fatal(err)
	}
	// Платёж НЕ помечается refunded: деньги не вернулись. Состояние
	// 'refunding' + refund_state='unknown' here НАМЕРЕННО и требуется CHECK-ом
	// payments_refund_state_transition_check (status='refunding' при любом
	// refund_state != 'none'): платёж остаётся «в процессе возврата» с
	// неизвестным исходом, чтобы его видел reconcile. Прежний тест утверждал
	// status='succeeded' и «проходил» только потому, что ветка не исполнялась.
	if status == "refunded" {
		t.Fatalf("failed telegram refund must not mark the payment refunded")
	}
	if status != "refunding" {
		t.Fatalf("parked payment must stay 'refunding' until reconcile, got %s", status)
	}
	if refundState == nil || *refundState != "unknown" {
		t.Fatalf("failed telegram refund must be recorded as 'unknown' for reconcile, got %v", refundState)
	}
	if lastError == nil || !strings.Contains(*lastError, "telegram") {
		t.Fatalf("refund_last_error must explain the telegram failure, got %v", lastError)
	}
	if reconReason == nil || *reconReason != "refund_unknown" {
		t.Fatalf("payment must be queued for reconcile, got reconciliation_reason=%v", reconReason)
	}
	// Возврат не подтверждён — подписку отзывать нельзя.
	var active int
	if err := pg.QueryRow(ctx,
		`SELECT count(*) FROM subscriptions WHERE user_id=$1 AND status='active'`, uid).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatalf("control: no subscription existed in this test, got %d active", active)
	}

	// Повторный запрос обязан честно сказать «нужен reconcile», а не выглядеть
	// как успешный возврат: иначе админ решит, что деньги вернулись.
	req2 := httptest.NewRequest("POST", "/v1/admin/refund", strings.NewReader(`{"payment_id":"`+pid+`"}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Admin-Token", "admin-token-0123456789abcdef")
	req2.RemoteAddr = "127.0.0.1:5555"
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("retry of an unknown refund must answer 409, got %d %s", rec2.Code, rec2.Body.String())
	}
}

// TestE2ERefundTelegramSuccessFinalizes — контр-случай к предыдущему тесту:
// когда Telegram подтверждает возврат, платёж обязан дойти до 'refunded'.
// Без него «исправление» в виде вечного 'unknown' прошло бы тесты.
func TestE2ERefundTelegramSuccessFinalizes(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	tgMock(t, 200, `{"ok":true,"result":true}`)
	t.Setenv("ADMIN_API_TOKEN", "admin-token-0123456789abcdef")
	r := refundRouter(t, pg, rd)

	uid := testutil.NewUser(t, ctx, pg)
	if _, err := pg.Exec(ctx, `UPDATE users SET tg_id=820002 WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	var pid string
	if err := pg.QueryRow(ctx, `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider,
		                      provider_payment_id, amount_rub, stars, status, idempotency_key)
		SELECT $1, id, 'month_299', 299, 'tg_stars', 'tg:ch_a14_ok', 299, 199, 'succeeded',
		       'a14-ok-' || gen_random_uuid()
		  FROM plans WHERE code='month_299' LIMIT 1 RETURNING id::text`, uid).Scan(&pid); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/v1/admin/refund", strings.NewReader(`{"payment_id":"`+pid+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Token", "admin-token-0123456789abcdef")
	req.RemoteAddr = "127.0.0.1:5555"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("confirmed telegram refund must answer 200, got %d %s", rec.Code, rec.Body.String())
	}
	var status string
	if err := pg.QueryRow(ctx, `SELECT status FROM payments WHERE id=$1`, pid).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "refunded" {
		t.Fatalf("confirmed telegram refund must finalize the payment, got %s", status)
	}
	var varJSON map[string]any
	if json.Unmarshal(rec.Body.Bytes(), &varJSON) != nil || varJSON["ok"] != true {
		t.Fatalf("want {\"ok\":true}, got %s", rec.Body.String())
	}
}

// TestRefundRouteIsWiredBehindRequireAdmin — сам роутер прод-сервиса живёт в
// package main и не импортируется, поэтому дублируем его проводку статически:
// снять RequireAdmin с /v1/admin/refund в cmd/admin/main.go обязан уронить этот
// тест. Иначе мутация прод-роута осталась бы незамеченной.
func TestRefundRouteIsWiredBehindRequireAdmin(t *testing.T) {
	src, err := os.ReadFile("../../cmd/admin/main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	for _, needle := range []string{
		`r.With(ad.RequireAdmin).Post("/v1/admin/refund", py.HandleRefund)`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("prod admin route must keep refund behind RequireAdmin; not found: %s", needle)
		}
	}
	// Никаких «голых» монтирований refund в прод-роутере.
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "/v1/admin/refund") &&
			!strings.Contains(line, "RequireAdmin") {
			t.Fatalf("refund route mounted without RequireAdmin: %s", strings.TrimSpace(line))
		}
	}
}
