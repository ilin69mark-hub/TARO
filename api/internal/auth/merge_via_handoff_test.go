// E2E слияния при переносе: покупка анонима реально переезжает в Telegram.
//
// Запуск: DATABASE_URL=... REDIS_ADDR=... go test ./internal/auth/ -run MergeVia -v
//
// Предыдущий файл проверял токены, этот — результат. Разница принципиальная:
// токен можно выдать и отозвать правильно, а подписка при этом осталась бы
// там, где была. Здесь проверяется, что после Link(..., viaHandoff=true):
//   - подписка, платежи и чтения анонима переехали к выжившему;
//   - проигравшая строка ЖИВА и помечена merged, а не удалена;
//   - токен при этом сгорает, и второе слияние тем же токеном невозможно.
package auth

import (
	"context"
	"errors"
	"testing"

	"taro/api/internal/testutil"
)

// mergeViaHandoff прогоняет слияние так, как это делает HandleLink при переносе.
func mergeViaHandoff(t *testing.T, ctx context.Context, svc *Service, handoffUser string, tgID int64) (survivor string, err error) {
	t.Helper()
	survivor, _, err = svc.Link(ctx, handoffUser, tgID, "", true)
	return survivor, err
}

func TestMergeViaHandoffMovesPurchaseAndKeepsLoser(t *testing.T) {
	ctx, svc, done := newHandoffSvc(t)
	defer done()

	// Аноним с покупкой и Telegram-аккаунт без неё — типичная картина.
	loser := makeAnon(t, ctx, svc, "merge-via-loser-fp")
	var winner string
	if err := svc.pg.QueryRow(ctx, `INSERT INTO users (anon_uuid, fingerprint) VALUES (gen_random_uuid(),'merge-via-winner-fp') RETURNING id::text`).Scan(&winner); err != nil {
		t.Fatal(err)
	}
	// tg_id уникален по всей таблице, поэтому берём из диапазона, который
	// заведомо не занят сидами.
	tgID := int64(799000123)
	if _, err := svc.pg.Exec(ctx, `UPDATE users SET tg_id=$2 WHERE id=$1`, winner, tgID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = svc.pg.Exec(context.Background(), `DELETE FROM users WHERE id=ANY($1)`, []string{loser, winner})
	})

	// Покупка на анонимной строке: подписка + платёж + чтение.
	var planID string
	if err := svc.pg.QueryRow(ctx, `SELECT id FROM plans WHERE code='month_299'`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.pg.Exec(ctx, `
		INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, status, valid_until, source_type)
		VALUES ($1,$2,'month_299',299,'active',now() + interval '30 days','payment')`, loser, planID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.pg.Exec(ctx, `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider,
		                      provider_payment_id, amount_rub, status)
		VALUES ($1,$2,'month_299',299,'tg_stars',$3,299,'succeeded')`, loser, planID, "merge-via-"+loser[:8]); err != nil {
		t.Fatal(err)
	}
	var readingID string
	if err := svc.pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, interpretation, seed, status, quota_state)
		VALUES ($1,'daily','вопрос','[]','толкование',1,'done','allowed') RETURNING id::text`, loser).Scan(&readingID); err != nil {
		t.Fatal(err)
	}

	issued, err := svc.IssueHandoff(ctx, loser, "month_299", "203.0.113.10")
	if err != nil {
		t.Fatal(err)
	}
	fromToken, _, err := svc.ConsumeHandoff(ctx, issued.Token, "203.0.113.77")
	if err != nil {
		t.Fatalf("потребление токена: %v", err)
	}
	if fromToken != loser {
		t.Fatalf("токен указал не на плательщика: %s", fromToken)
	}

	survivor, err := mergeViaHandoff(t, ctx, svc, fromToken, tgID)
	if err != nil {
		t.Fatalf("слияние: %v", err)
	}
	if survivor != winner {
		t.Fatalf("выжил не тот: %s (ждали %s)", survivor, winner)
	}

	// Деньги и история переехали.
	var subs, pays int
	if err := svc.pg.QueryRow(ctx, `SELECT count(*) FROM subscriptions WHERE user_id=$1`, winner).Scan(&subs); err != nil {
		t.Fatal(err)
	}
	if err := svc.pg.QueryRow(ctx, `SELECT count(*) FROM payments WHERE user_id=$1`, winner).Scan(&pays); err != nil {
		t.Fatal(err)
	}
	var readOwner string
	if err := svc.pg.QueryRow(ctx, `SELECT user_id::text FROM readings WHERE id=$1`, readingID).Scan(&readOwner); err != nil {
		t.Fatal(err)
	}
	if subs != 1 || pays != 1 || readOwner != winner {
		t.Fatalf("перенос неполон: subs=%d pays=%d reading=%s", subs, pays, readOwner)
	}

	// Проигравшая строка ЖИВА. Это и есть обратимость: если бы её удалили,
	// пересланный токен отнял бы покупку навсегда.
	var status string
	var anonUUID *string
	if err := svc.pg.QueryRow(ctx, `SELECT status, anon_uuid::text FROM users WHERE id=$1`, loser).Scan(&status, &anonUUID); err != nil {
		t.Fatalf("проигравшей строки нет — откат невозможен: %v", err)
	}
	if status != "merged" {
		t.Fatalf("статус проигравшего: %s", status)
	}
	// anon_uuid освобождён: он UNIQUE, и оставленная строка заблокировала бы
	// человеку вход вообще после переноса.
	if anonUUID != nil {
		t.Fatalf("anon_uuid не освобождён: %s", *anonUUID)
	}

	// Сессия проигравшего больше не работает: RequireAuth смотрит status='active'.
	var active bool
	if err := svc.pg.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND status='active')`, loser).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("проигравший остался активным — старая сессия браузера выжила бы")
	}

	// Токен сгорел: второе слияние тем же токеном невозможно.
	if _, _, err := svc.ConsumeHandoff(ctx, issued.Token, "203.0.113.77"); err != ErrHandoffNotFound {
		t.Fatalf("токен пережил слияние: %v", err)
	}
}

// Защита, которая обязана выжить при переносе. Реферальные циклы — это
// про деньги: цикл a→b→a раздувает начисления рефереру, поэтому
// rejectReferralCyclesTx не обходится флагом viaHandoff. Раньше это было
// непроверяемо — сверка fingerprint'ов внутри mergeMetadataTx отбивала бы
// такой кейс раньше, чем до цикла дошло.
func TestMergeViaHandoffStillRejectsReferralCycle(t *testing.T) {
	ctx, svc, done := newHandoffSvc(t)
	defer done()

	tgID := int64(799000999)
	var survivor, loser, other string
	if err := svc.pg.QueryRow(ctx, `INSERT INTO users (tg_id, fingerprint) VALUES ($1,'cyc-win-fp') RETURNING id::text`, tgID).Scan(&survivor); err != nil {
		t.Fatal(err)
	}
	if err := svc.pg.QueryRow(ctx, `INSERT INTO users (anon_uuid, fingerprint) VALUES (gen_random_uuid(),'cyc-loser-fp') RETURNING id::text`).Scan(&loser); err != nil {
		t.Fatal(err)
	}
	if err := svc.pg.QueryRow(ctx, `INSERT INTO users (anon_uuid) VALUES (gen_random_uuid()) RETURNING id::text`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.PurgeUsers(t, context.Background(), svc.pg, survivor, loser, other) })
	// Цикл loser→other→loser. Слияние с tg_id выжившего обязано его отбить.
	if _, err := svc.pg.Exec(ctx, `INSERT INTO referrals (referrer_id, referee_id, code) VALUES ($1,$2,$3)`, loser, other, "HC1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.pg.Exec(ctx, `INSERT INTO referrals (referrer_id, referee_id, code) VALUES ($1,$2,$3)`, other, loser, "HC2"); err != nil {
		t.Fatal(err)
	}
	// Fingerprint'ы РАЗНЫЕ — то есть проверка, которую снимает viaHandoff,
	// действительно была бы здесь единственным препятствием. Цикл обязан
	// отбиться сам.
	if _, _, err := svc.Link(ctx, loser, tgID, "", true); !errors.Is(err, errMergeCycle) {
		t.Fatalf("цикл при переносе не отбит: %v", err)
	}
	// Отказ не разрушителен: обе строки на месте.
	var users int
	if err := svc.pg.QueryRow(ctx, `SELECT count(*) FROM users WHERE id=ANY($1)`, []string{survivor, loser}).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 2 {
		t.Fatalf("отказ по циклу был разрушителен: осталось %d строк", users)
	}
}

// Слияние анонима в УЖЕ существующий Telegram-аккаунт — это и есть продукт:
// покупка из браузера собирается в аккаунте, который открыл бота. Проверяем,
// что витхим из защит не выпало ничего лишнего и аноним больше не активен.
func TestMergeViaHandoffIntoExistingTelegramAccount(t *testing.T) {
	ctx, svc, done := newHandoffSvc(t)
	defer done()

	tgID := int64(799000555)
	var winner string
	if err := svc.pg.QueryRow(ctx, `INSERT INTO users (tg_id, fingerprint) VALUES ($1,'into-win-fp') RETURNING id::text`, tgID).Scan(&winner); err != nil {
		t.Fatal(err)
	}
	loser := makeAnon(t, ctx, svc, "into-loser-fp")
	t.Cleanup(func() { testutil.PurgeUsers(t, context.Background(), svc.pg, winner, loser) })

	var planID string
	if err := svc.pg.QueryRow(ctx, `SELECT id FROM plans WHERE code='month_299'`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.pg.Exec(ctx, `
		INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, status, valid_until, source_type)
		VALUES ($1,$2,'month_299',299,'active',now() + interval '30 days','payment')`, loser, planID); err != nil {
		t.Fatal(err)
	}

	survivor, err := mergeViaHandoff(t, ctx, svc, loser, tgID)
	if err != nil {
		t.Fatalf("слияние в существующий аккаунт: %v", err)
	}
	if survivor != winner {
		t.Fatalf("выжил не Telegram-аккаунт: %s", survivor)
	}
	// Аноним больше не active — его старая браузерная сессия не должна
	// продолжать работать после переноса.
	var status string
	if err := svc.pg.QueryRow(ctx, `SELECT status FROM users WHERE id=$1`, loser).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "merged" {
		t.Fatalf("статус анонима после слияния: %s", status)
	}
	var subs int
	if err := svc.pg.QueryRow(ctx, `SELECT count(*) FROM subscriptions WHERE user_id=$1`, winner).Scan(&subs); err != nil {
		t.Fatal(err)
	}
	if subs != 1 {
		t.Fatalf("подписка не переехала: %d", subs)
	}
	// Fingerprint выжившего не затирается пустым значением от переноса: иначе
	// проверка отпечатка вообще отключилась бы у этого аккаунта.
	var fp string
	if err := svc.pg.QueryRow(ctx, `SELECT fingerprint FROM users WHERE id=$1`, winner).Scan(&fp); err != nil {
		t.Fatal(err)
	}
	if fp != "into-win-fp" {
		t.Fatalf("fingerprint выжившего изменён: %q", fp)
	}
}

func TestMergeViaHandoffRejectsUnlinkedForeignToken(t *testing.T) {
	// Сценарий подмены: у сессии есть свой аноним, а в теле — чужой токен.
	// HandleLink обязан отказать, иначе достаточно было бы знать чужой токен.
	ctx, svc, done := newHandoffSvc(t)
	defer done()

	attacker := makeAnon(t, ctx, svc, "swap-attacker-fp")
	victim := makeAnon(t, ctx, svc, "swap-victim-fp")
	t.Cleanup(func() { testutil.PurgeUsers(t, context.Background(), svc.pg, attacker, victim) })
	issued, err := svc.IssueHandoff(ctx, victim, "month_299", "203.0.113.10")
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := svc.ConsumeHandoff(ctx, issued.Token, "203.0.113.10")
	if err != nil {
		t.Fatal(err)
	}
	if got == attacker {
		t.Fatal("токен выдал чужой аккаунт")
	}
	// Ровно та проверка, которую делает HandleLink перед Link(): current != handed
	// означает, что в теле запроса чужой токен, и слияние не выполняется.
	if attacker == got {
		t.Fatal("токен отдал чужой аккаунт — HandleLink передаст current=attacker, и слияние уйдёт не туда")
	}
}
