// Читатель payment_webhook_events (A09/F-09). До этой задачи таблицу писал
// только код вебхуков, а читателей не существовало: дубликаты списаний и
// расхождения фиксировались и оставались незамеченными. Тесты держат два
// свойства: (1) нужные записи находятся, (2) нормальный первый вебхук без
// привязанного Telegram (owner_unverified) не поднимает тревогу.
package payments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"taro/api/internal/admin"
	"taro/api/internal/me"
	"taro/api/internal/testutil"
)

// insertWebhookEvent кладёт событие так же, как это делает recordWebhookEvent:
// те же поля, тот же формат event_hash, то же ограничение уникальности.
var tgCounter atomic.Int64

func insertWebhookEvent(t *testing.T, ctx context.Context, pg *pgxpool.Pool, paymentID, reason string, at time.Time) {
	t.Helper()
	h := sha256.Sum256([]byte(paymentID + "|" + reason + "|" + at.String()))
	if _, err := pg.Exec(ctx, `
		INSERT INTO payment_webhook_events
			(payment_id, event_hash, reason, currency, total_amount, telegram_charge_id,
			 provider_charge_id, owner_tg_id, created_at)
		VALUES ($1, $2, $3, 'XTR', 199, $4, $5, 4242, $6)`,
		paymentID, hex.EncodeToString(h[:]), reason, "ch_"+reason, "pch_"+reason, at); err != nil {
		t.Fatalf("insert webhook event %s: %v", reason, err)
	}
}

func newPayWithUser(t *testing.T, ctx context.Context, pg *pgxpool.Pool, key string) (string, string) {
	t.Helper()
	// tg_id уникален (idx_users_tg), поэтому на каждый вызов — своё значение.
	tg := tgCounter.Add(1) + 4200
	var uid string
	if err := pg.QueryRow(ctx,
		`INSERT INTO users (anon_uuid, tg_id) VALUES (gen_random_uuid(), $1) RETURNING id`, tg).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, uid) })
	var payID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider,
		                      provider_payment_id, amount_rub, stars, status, idempotency_key)
		SELECT $1, id, 'month_299', 299, 'tg_stars', 'a09:' || gen_random_uuid(), 299, 199, 'succeeded', $2
		  FROM plans WHERE code='month_299' ORDER BY valid_from DESC LIMIT 1
		RETURNING id::text`, uid, key).Scan(&payID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM payment_webhook_events WHERE payment_id=$1`, payID)
		_, _ = pg.Exec(context.Background(), `DELETE FROM payments WHERE id=$1`, payID)
	})
	return payID, uid
}

// findItem ищет группу по своему платежу. Ассерты намеренно scoped к фикстуре:
// таблица общая для всего пакета, и другие тесты (вебхуки) оставляют в ней свои
// события — глобальные счётчики сделали бы тест зависимым от порядка прогонов.
// Это тот же класс дефекта, что F-58 (drainOnce забирает чужое).
func findItem(items []WebhookMismatch, paymentID, reason string) *WebhookMismatch {
	for i := range items {
		if items[i].PaymentID == paymentID && items[i].Reason == reason {
			return &items[i]
		}
	}
	return nil
}

func TestReconcileFindsDuplicateCharge(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	payID, uid := newPayWithUser(t, ctx, pg, "a09-dup-"+t.Name())

	now := time.Now()
	insertWebhookEvent(t, ctx, pg, payID, ReasonDuplicateCharge, now.Add(-2*time.Hour))
	insertWebhookEvent(t, ctx, pg, payID, ReasonDuplicateCharge, now.Add(-1*time.Hour))
	insertWebhookEvent(t, ctx, pg, payID, ReasonOwnerUnverified, now.Add(-30*time.Minute))

	items, err := ReconcileWebhookEvents(ctx, pg, now.Add(-48*time.Hour), 1000)
	if err != nil {
		t.Fatal(err)
	}
	found := findItem(items, payID, ReasonDuplicateCharge)
	if found == nil {
		t.Fatalf("duplicate_charge not reported for %s", payID)
	}
	if found.Events != 2 {
		t.Fatalf("expected 2 grouped duplicate_charge events, got %d", found.Events)
	}
	if found.UserID != uid {
		t.Fatalf("group lost the user: got %q want %q", found.UserID, uid)
	}
	if found.PaymentStatus != "succeeded" || found.AmountRub != 299 {
		t.Fatalf("payment context lost: status=%q amount=%d", found.PaymentStatus, found.AmountRub)
	}
	if !found.NeedsAttention() {
		t.Fatal("duplicate_charge must require attention")
	}
	if findItem(items, payID, ReasonOwnerUnverified) == nil {
		t.Fatal("owner_unverified event of the same payment was not reported")
	}

	attention, byReason := WebhookEventSummary(items)
	if attention < 1 {
		t.Fatal("summary must count duplicate_charge as needing attention")
	}
	if byReason[ReasonDuplicateCharge] < 1 {
		t.Fatalf("by_reason must count duplicate_charge: %v", byReason)
	}
}

func TestReconcileOwnerUnverifiedIsNotAnIncident(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	payID, _ := newPayWithUser(t, ctx, pg, "a09-owner-"+t.Name())
	insertWebhookEvent(t, ctx, pg, payID, ReasonOwnerUnverified, time.Now())

	items, err := ReconcileWebhookEvents(ctx, pg, time.Now().Add(-time.Hour), 1000)
	if err != nil {
		t.Fatal(err)
	}
	mine := findItem(items, payID, ReasonOwnerUnverified)
	if mine == nil {
		t.Fatal("owner_unverified row of the test payment not reported")
	}
	if mine.NeedsAttention() {
		t.Fatal("owner_unverified is a normal first webhook, not an incident")
	}
	// В сводке этот платёж не должен давать тревогу: суммируем только свои строки.
	var ownAttention int
	for _, m := range items {
		if m.PaymentID == payID && m.NeedsAttention() {
			ownAttention++
		}
	}
	if ownAttention != 0 {
		t.Fatalf("own rows needing attention: %d, want 0", ownAttention)
	}
}

func TestReconcileRespectsSinceAndLimit(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	old, _ := newPayWithUser(t, ctx, pg, "a09-old-"+t.Name())
	fresh, _ := newPayWithUser(t, ctx, pg, "a09-fresh-"+t.Name())
	insertWebhookEvent(t, ctx, pg, old, ReasonChargeReused, time.Now().Add(-72*time.Hour))
	insertWebhookEvent(t, ctx, pg, fresh, ReasonChargeReused, time.Now().Add(-time.Hour))

	items, err := ReconcileWebhookEvents(ctx, pg, time.Now().Add(-24*time.Hour), 1000)
	if err != nil {
		t.Fatal(err)
	}
	if findItem(items, fresh, ReasonChargeReused) == nil {
		t.Fatal("fresh event must be inside the window")
	}
	if findItem(items, old, ReasonChargeReused) != nil {
		t.Fatal("--since must hide events older than the window")
	}
	limited, err := ReconcileWebhookEvents(ctx, pg, time.Unix(0, 0), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 {
		t.Fatalf("limit must cap rows, got %d", len(limited))
	}
}

// Серверный путь админ-API: X-Admin-Token с loopback — ровно тот способ, которым
// будет пользоваться мониторинг. Если бы ручной читатель жил только в CLI,
// находка F-09 повторилась бы для HTTP-контроля.
func TestAdminWebhookAuditEndpointReadsTheSameData(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	t.Setenv("ADMIN_API_TOKEN", "a09-token")
	ad := admin.New(pg, rd)
	svc := New(pg, me.New(pg, rd))
	payID, _ := newPayWithUser(t, ctx, pg, "a09-ep-"+t.Name())
	insertWebhookEvent(t, ctx, pg, payID, ReasonOwnerMismatch, time.Now().Add(-time.Hour))

	r := chi.NewRouter()
	r.With(ad.RequireAdmin).Get("/v1/admin/payments/audit", svc.HandleAdminWebhookAudit)

	since := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	req := httptest.NewRequest("GET", "/v1/admin/payments/audit?since="+since, nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("X-Admin-Token", "a09-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit endpoint: %d %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Rows           int               `json:"rows"`
		NeedsAttention int               `json:"needs_attention"`
		ByReason       map[string]int    `json:"by_reason"`
		Items          []WebhookMismatch `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if payload.Rows == 0 || payload.NeedsAttention < 1 {
		t.Fatalf("endpoint must surface the incident: %+v", payload)
	}
	if payload.ByReason[ReasonOwnerMismatch] < 1 {
		t.Fatalf("by_reason must count owner_mismatch: %+v", payload.ByReason)
	}

	// авторизация обязательна: без токена и не с loopback — 403
	bad := httptest.NewRequest("GET", "/v1/admin/payments/audit", nil)
	bad.RemoteAddr = testutil.UniqueIP(t) + ":5555"
	bad.Header.Set("X-Admin-Token", "a09-token")
	badRec := httptest.NewRecorder()
	r.ServeHTTP(badRec, bad)
	if badRec.Code != http.StatusForbidden {
		t.Fatalf("non-loopback admin token must be rejected, got %d", badRec.Code)
	}
}
