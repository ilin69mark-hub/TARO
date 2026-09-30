// Сквозная проверка A12/F-12: терминальный провал в воркере возвращает
// списанный entitlement. Провайдер здесь не используется — обе терминальные
// ветки (исчерпание попыток и markFailed) проверяются детерминированно:
// сценарий со сталящим провайдером зависит от таймингов HTTP и не даёт
// стабильного теста.
package ai

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"taro/api/internal/entitlements"
	"taro/api/internal/testutil"
)

// TestWorkerReleasesEntitlementOnExhaustedAttempts — ветка «попытки исчерпаны»
// в drainOnce: entitlement обязан вернуться вместе с переводом в failed.
func TestWorkerReleasesEntitlementOnExhaustedAttempts(t *testing.T) {
	t.Setenv("AI_WORKER_MAX_ATTEMPTS", "2")
	ctx, pg, rd := testutil.Live(t)
	en := entitlements.New(pg, rd)
	uid := testutil.NewUser(t, ctx, pg)

	var readingID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, seed, algo_version,
		                      status, interpretation, quota_state)
		VALUES ($1,'daily','', '[{"card_id":1,"reversed":false,"position":0}]',1,1,'pending','','unchecked')
		RETURNING id::text`, uid).Scan(&readingID); err != nil {
		t.Fatal(err)
	}
	if v, err := en.AuthorizeReading(ctx, readingID, uid, "daily"); err != nil || !v.Allow {
		t.Fatalf("authorize: %+v %v", v, err)
	}
	// Имитируем исчерпание попыток (например, сталив провайдера): счётчик
	// упёрся в лимит, lease истёк — воркер вправе забрать и не может ждать.
	if _, err := pg.Exec(ctx,
		`UPDATE readings SET worker_attempts=$1, worker_lease_until=NULL WHERE id=$2`, 2, readingID); err != nil {
		t.Fatal(err)
	}

	gw := New(pg, rd)
	// Повторяем drain до terminal-перехода своего чтения: drainOnce забирает
	// ЛЮБОЕ подходящее чтение (F-58), и при параллельной работе пакетов первый
	// вызов может уйти на чужую строку.
	deadline := time.Now().Add(15 * time.Second)
	for {
		gw.drainOnce(ctx, pg)
		var st string
		if err := pg.QueryRow(ctx, `SELECT status FROM readings WHERE id=$1`, readingID).Scan(&st); err != nil {
			t.Fatal(err)
		}
		if st == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("чтение не перешло в failed (status=%s)", st)
		}
		time.Sleep(50 * time.Millisecond)
	}

	assertFailedAndReleased(t, ctx, pg, uid, readingID)
}

// TestWorkerReleasesEntitlementOnMarkFailed — ветка markFailed: claim выдан,
// попытка провалилась, терминальный переход возвращает entitlement.
func TestWorkerReleasesEntitlementOnMarkFailed(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	en := entitlements.New(pg, rd)
	uid := testutil.NewUser(t, ctx, pg)

	var readingID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, seed, algo_version,
		                      status, interpretation, quota_state)
		VALUES ($1,'daily','', '[{"card_id":1,"reversed":false,"position":0}]',1,1,'pending','','unchecked')
		RETURNING id::text`, uid).Scan(&readingID); err != nil {
		t.Fatal(err)
	}
	if v, err := en.AuthorizeReading(ctx, readingID, uid, "daily"); err != nil || !v.Allow {
		t.Fatalf("authorize: %+v %v", v, err)
	}
	// Claim отдан воркеру: читаем реальный токен из claim-условия.
	if _, err := pg.Exec(ctx, `
		UPDATE readings
		   SET worker_claim_token=gen_random_uuid(),
		       worker_lease_until=now()+interval '1 minute',
		       worker_attempts=0
		 WHERE id=$1`, readingID); err != nil {
		t.Fatal(err)
	}
	var token string
	if err := pg.QueryRow(ctx, `SELECT worker_claim_token::text FROM readings WHERE id=$1`, readingID).Scan(&token); err != nil {
		t.Fatal(err)
	}

	gw := New(pg, rd)
	gw.markFailed(pg, readingID, token)

	assertFailedAndReleased(t, ctx, pg, uid, readingID)
}

func assertFailedAndReleased(t *testing.T, ctx context.Context, pg *pgxpool.Pool, uid, readingID string) {
	t.Helper()
	var status, reason string
	var failedAt *time.Time
	if err := pg.QueryRow(ctx,
		`SELECT status, COALESCE(failure_reason,''), failed_at FROM readings WHERE id=$1`,
		readingID).Scan(&status, &reason, &failedAt); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("reading must be terminally failed, got %q", status)
	}
	// DoD карточки: у провала обязана быть записана причина, иначе клиент
	// не может отличить «провайдер не ответил» от «что-то другое» и не знает,
	// что ретрай бесплатный.
	if reason == "" {
		t.Fatal("terminal failure must record failure_reason")
	}
	if failedAt == nil {
		t.Fatal("terminal failure must record failed_at")
	}
	var used int
	if err := pg.QueryRow(ctx, `SELECT free_used_today FROM entitlements WHERE user_id=$1`, uid).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != 0 {
		t.Fatalf("entitlement not released after terminal failure: free_used_today=%d", used)
	}
	var releasedAt *time.Time
	if err := pg.QueryRow(ctx,
		`SELECT released_at FROM reading_authorization_receipts WHERE reading_id=$1`, readingID).Scan(&releasedAt); err != nil {
		t.Fatal(err)
	}
	if releasedAt == nil {
		t.Fatal("receipt must record the release (released_at), otherwise it is invisible")
	}
}
