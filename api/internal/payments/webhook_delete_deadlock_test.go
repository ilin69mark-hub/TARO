// Дедлок вебхук ↔ DELETE /v1/me (A08/F-08).
//
// Порядок блокировок до фикса: вебхук брал payments, затем вставка в
// subscriptions требовала KEY SHARE на users через FK — payments → users.
// DELETE /v1/me шёл users → payments (ON DELETE CASCADE). Встречные порядки
// дают 40P01; PostgreSQL всегда отменял DELETE, то есть пользователь получал
// 500 и принудительный повторный логин поверх живой учётной записи.
//
// Тест измеряет это напрямую счётчиком pg_stat_database.deadlocks, а не
// HTTP-кодами: код 500 может прийти и по другой причине, а дедлок — всегда
// ровно один deadlock aborted.
package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"taro/api/internal/auth"
	"taro/api/internal/me"
	"taro/api/internal/testutil"
)

func a08DeadlockCount(t *testing.T, ctx context.Context, pg *pgxpool.Pool) int64 {
	t.Helper()
	var n int64
	if err := pg.QueryRow(ctx,
		`SELECT deadlocks FROM pg_stat_database WHERE datname = current_database()`).Scan(&n); err != nil {
		t.Fatalf("read deadlocks counter: %v", err)
	}
	return n
}

// a08SlowPaymentUpdate триггер задерживает UPDATE payments — это точка между
// захватом строки payments и вставкой в subscriptions, где и возникает цикл.
// Без задержки окно слишком узкое (проверено: гонка не ловится стабильно).
func a08SlowPaymentUpdate(t *testing.T, ctx context.Context, pg *pgxpool.Pool) {
	t.Helper()
	const fn = "taro_test_slow_pay_update"
	if _, err := pg.Exec(ctx, `
		CREATE OR REPLACE FUNCTION `+fn+`() RETURNS trigger AS $f$
		BEGIN
			PERFORM pg_sleep(0.4);
			RETURN NEW;
		END $f$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create fn: %v", err)
	}
	if _, err := pg.Exec(ctx, `DROP TRIGGER IF EXISTS taro_test_slow_pay ON payments`); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `
		CREATE TRIGGER taro_test_slow_pay BEFORE UPDATE ON payments
		FOR EACH ROW EXECUTE FUNCTION `+fn+`()`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pg.Exec(bg, `DROP TRIGGER IF EXISTS taro_test_slow_pay ON payments`)
		_, _ = pg.Exec(bg, `DROP FUNCTION IF EXISTS `+fn+`()`)
	})
}

func TestWebhookVsDeleteNoDeadlock(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	t.Setenv("TG_STARS_SECRET_TOKEN", "test-secret")

	svc := New(pg, me.New(pg, rd))
	au := auth.New(pg, rd)
	mv := me.New(pg, rd)
	webhookRouter := chi.NewRouter()
	webhookRouter.Post("/v1/payments/stars/webhook", svc.HandleWebhook)
	deleteRouter := http.NewServeMux()
	deleteRouter.Handle("/v1/me", au.RequireAuth(http.HandlerFunc(mv.HandleDelete)))

	a08SlowPaymentUpdate(t, ctx, pg)
	before := a08DeadlockCount(t, ctx, pg)

	const iters = 50
	var deleteOK, deleteFatal int
	for i := 0; i < iters; i++ {
		var uid string
		if err := pg.QueryRow(ctx,
			`INSERT INTO users (anon_uuid, tg_id, age_confirmed_at)
			 VALUES (gen_random_uuid(), 4200 + $1, now()) RETURNING id`,
			i).Scan(&uid); err != nil {
			t.Fatal(err)
		}
		tok, err := auth.IssueJWT(uid, auth.UserTTL)
		if err != nil {
			t.Fatal(err)
		}
		if err := rd.Set(ctx, "sess:"+uid, "x", auth.UserTTL).Err(); err != nil {
			t.Fatal(err)
		}
		var payID string
		if err := pg.QueryRow(ctx, `
			INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider,
			                      provider_payment_id, amount_rub, stars, status, idempotency_key)
			SELECT $1, id, 'month_299', 299, 'tg_stars', 'a08:' || gen_random_uuid(), 299, 199, 'pending', $2
			  FROM plans WHERE code='month_299' ORDER BY valid_from DESC LIMIT 1
			RETURNING id::text`, uid, fmt.Sprintf("a08-%d", i)).Scan(&payID); err != nil {
			t.Fatal(err)
		}
		body := `{"message":{"from":{"id":` + fmt.Sprint(4200+i) +
			`},"successful_payment":{"currency":"XTR","total_amount":199,"invoice_payload":"` + payID +
			`","telegram_payment_charge_id":"ch_a08_` + fmt.Sprint(i) +
			`","provider_payment_charge_id":"pch_a08_` + fmt.Sprint(i) + `"}}}`

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/v1/payments/stars/webhook", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "test-secret")
			rec := httptest.NewRecorder()
			webhookRouter.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("iter %d: webhook %d %s", i, rec.Code, rec.Body.String())
			}
		}()
		var delRec *httptest.ResponseRecorder
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("DELETE", "/v1/me", strings.NewReader(`{"confirm":"DELETE"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF", "test")
			req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
			delRec = httptest.NewRecorder()
			deleteRouter.ServeHTTP(delRec, req)
		}()
		wg.Wait()

		switch delRec.Code {
		case http.StatusOK:
			deleteOK++
		case http.StatusUnprocessableEntity, http.StatusUnauthorized, http.StatusNotFound:
			// ожидаемые отказы: конфликт состояния, нет прав, юзера уже нет
		default:
			// 500 = транзакция упала; до фикса это 40P01 из-за вебхука
			deleteFatal++
			if i < 3 {
				var payload map[string]any
				_ = json.Unmarshal(delRec.Body.Bytes(), &payload)
				t.Logf("iter %d: DELETE /v1/me -> %d %s", i, delRec.Code, delRec.Body.String())
			}
		}
		// юзер мог остаться (delete не прошёл) — убираем руками
		_, _ = pg.Exec(ctx, `DELETE FROM users WHERE id=$1`, uid)
		_, _ = pg.Exec(ctx, `DELETE FROM payments WHERE id=$1`, payID)
	}

	after := a08DeadlockCount(t, ctx, pg)
	if after > before {
		t.Fatalf("deadlocks happened: counter %d → %d (delete 500 in %d of %d iterations)",
			before, after, deleteFatal, iters)
	}
	if deleteFatal > 0 {
		t.Fatalf("DELETE /v1/me returned 500 in %d of %d iterations (no deadlocks counted, but the path is broken)", deleteFatal, iters)
	}
	t.Logf("ok: %d iterations, no deadlocks (counter %d), delete ok=%d fatal=%d", iters, after, deleteOK, deleteFatal)
}
