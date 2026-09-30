// Регресс атак на антиферму рефералки (аудит P1–P8, миграция 037).
//
// Каждый тест соответствует конкретному обходу, который был закрыт. Если
// тест снова упадёт — соответствующая защита отвалилась.
package referral

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"taro/api/internal/testutil"
)

func mkUser(t *testing.T, ctx context.Context, pg *pgxpool.Pool, tg *int64, fp, ip string) string {
	t.Helper()
	// IP кладём хэшем — ровно так же, как это делает прод (HandleMe -> hashIP).
	// Сырой адрес в referrals/users не хранится: он нужен только для сравнения.
	var id string
	var err error
	if tg != nil {
		err = pg.QueryRow(ctx,
			`INSERT INTO users (tg_id, fingerprint, referral_ip) VALUES ($1,$2,$3) RETURNING id`,
			*tg, fp, hashIP(ip)).Scan(&id)
	} else {
		err = pg.QueryRow(ctx,
			`INSERT INTO users (anon_uuid, fingerprint, referral_ip) VALUES (gen_random_uuid(),$1,$2) RETURNING id`,
			fp, hashIP(ip)).Scan(&id)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id) })
	return id
}

func i64(v int64) *int64 { return &v }

// setReferralCfg подменяет app_config 'referral' на время теста и возвращает
// исходное значение. Тесты меняют глобальный конфиг, поэтому без отката они
// ломали бы друг друга при параллельном прогоне пакета.
func setReferralCfg(t *testing.T, ctx context.Context, pg *pgxpool.Pool, value string) {
	t.Helper()
	var prev []byte
	err := pg.QueryRow(ctx, `SELECT value FROM app_config WHERE key='referral'`).Scan(&prev)
	t.Cleanup(func() {
		if err != nil {
			_, _ = pg.Exec(context.Background(),
				`INSERT INTO app_config (key, value) VALUES ('referral', $1::jsonb)`, value)
			return
		}
		_, _ = pg.Exec(context.Background(),
			`INSERT INTO app_config (key, value) VALUES ('referral', $1::jsonb)
			 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, prev)
	})
	if _, err := pg.Exec(ctx,
		`INSERT INTO app_config (key, value) VALUES ('referral', $1::jsonb)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, value); err != nil {
		t.Fatal(err)
	}
}

func withCode(t *testing.T, ctx context.Context, pg *pgxpool.Pool, id, code string) {
	t.Helper()
	if _, err := pg.Exec(ctx, `UPDATE users SET referral_code=$1 WHERE id=$2`, code, id); err != nil {
		t.Fatal(err)
	}
}

func seedPending(t *testing.T, ctx context.Context, pg *pgxpool.Pool, referrer, referee, code string) {
	t.Helper()
	if _, err := pg.Exec(ctx, `
		INSERT INTO referrals (referrer_id, referee_id, code, status, bonus_days)
		VALUES ($1,$2,$3,'pending',3) ON CONFLICT (referee_id) DO NOTHING`,
		referrer, referee, code); err != nil {
		t.Fatal(err)
	}
}

func referralStatus(t *testing.T, ctx context.Context, pg *pgxpool.Pool, referee string) (string, string) {
	t.Helper()
	var st, reason string
	if err := pg.QueryRow(ctx,
		`SELECT status, COALESCE(reject_reason,'') FROM referrals WHERE referee_id=$1`, referee).
		Scan(&st, &reason); err != nil {
		t.Fatal(err)
	}
	return st, reason
}

func referrerMonth(t *testing.T, ctx context.Context, pg *pgxpool.Pool, referrer string) int {
	t.Helper()
	var m int
	_ = pg.QueryRow(ctx,
		`SELECT COALESCE(referral_bonus_month,0) FROM entitlements WHERE user_id=$1`, referrer).Scan(&m)
	return m
}

// P1 (было): apply чужого кода молча проглатывался и юзеру уходило 200.
// Теперь 409 ALREADY_REFERRED и привязка не переписывается.
func TestP1SecondCodeIsRejectedNotSilentlySwallowed(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	r, login := testRouter(t)
	refA := testutil.NewUser(t, ctx, pg)
	refB := testutil.NewUser(t, ctx, pg)
	referee := testutil.NewUser(t, ctx, pg)

	withCode(t, ctx, pg, refA, "P1AAAAAA")
	withCode(t, ctx, pg, refB, "P1BBBBBB")
	tok := login(referee)

	rec := callWith(r, tok, "POST", "/v1/referral/apply", `{"code":"P1BBBBBB"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("first apply: want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	// Второй apply с ДРУГИМ кодом обязан быть 409, иначе юзер думает, что
	// привязан к B, а привязан к A.
	rec2 := callWith(r, tok, "POST", "/v1/referral/apply", `{"code":"P1AAAAAA"}`)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("second code: want 409 got %d: %s", rec2.Code, rec2.Body.String())
	}
	var boundB int
	_ = pg.QueryRow(ctx, `SELECT COUNT(*) FROM referrals WHERE referee_id=$1 AND referrer_id=$2`, referee, refB).Scan(&boundB)
	var boundA int
	_ = pg.QueryRow(ctx, `SELECT COUNT(*) FROM referrals WHERE referee_id=$1 AND referrer_id=$2`, referee, refA).Scan(&boundA)
	if boundB != 1 || boundA != 0 {
		t.Fatalf("binding changed by 409: boundA=%d boundB=%d", boundA, boundB)
	}
}

// P2 (было): отказ reason=anon навсегда блокировал apply — человек применил код
// до входа через Telegram, прочитал расклад анонимом, получил rejected и больше
// код применить не мог. Теперь reason=anon переигрывается после входа.
func TestP2AnonRejectionCanBeReplayedAfterTelegramLogin(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	r, login := testRouter(t)
	referrer := testutil.NewUser(t, ctx, pg)
	referee := testutil.NewUser(t, ctx, pg)
	withCode(t, ctx, pg, referrer, "P2REFERR")
	tok := login(referee)

	// Отказ как будто случился до входа через TG.
	if _, err := pg.Exec(ctx, `
		INSERT INTO referrals (referrer_id, referee_id, code, status, bonus_days, reject_reason)
		VALUES ($1,$2,'P2OLD','rejected',3,'anon')`, referrer, referee); err != nil {
		t.Fatal(err)
	}
	// Пока TG нет — переигровка запрещена (иначе сжигаем попытку впустую).
	rec := callWith(r, tok, "POST", "/v1/referral/apply", `{"code":"P2REFERR"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("replay without tg: want 409 got %d: %s", rec.Code, rec.Body.String())
	}
	// Вход через Telegram.
	var tg int64 = 770001
	if _, err := pg.Exec(ctx, `UPDATE users SET tg_id=$1 WHERE id=$2`, tg, referee); err != nil {
		t.Fatal(err)
	}
	rec2 := callWith(r, tok, "POST", "/v1/referral/apply", `{"code":"P2REFERR"}`)
	if rec2.Code != http.StatusOK {
		t.Fatalf("replay with tg: want 200 got %d: %s", rec2.Code, rec2.Body.String())
	}
	st, reason := referralStatus(t, ctx, pg, referee)
	if st != "pending" || reason != "" {
		t.Fatalf("after replay: status=%q reason=%q", st, reason)
	}
	// Хук должен доехать до completed.
	svc := New(pg, nil)
	svc.CompleteOnFirstReading(ctx, referee)
	st, _ = referralStatus(t, ctx, pg, referee)
	if st != "completed" {
		t.Fatalf("replay must complete, status=%q", st)
	}
}

// Отказ по АНТИФЕРМЕ переигрывать нельзя: иначе перебор просто повторяют.
func TestP2bAntifarmRejectionIsPermanent(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	r, login := testRouter(t)
	referrer := testutil.NewUser(t, ctx, pg)
	referee := testutil.NewUser(t, ctx, pg)
	withCode(t, ctx, pg, referrer, "P2BREFER")
	tok := login(referee)
	var tg int64 = 770002
	if _, err := pg.Exec(ctx, `UPDATE users SET tg_id=$1 WHERE id=$2`, tg, referee); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `
		INSERT INTO referrals (referrer_id, referee_id, code, status, bonus_days, reject_reason)
		VALUES ($1,$2,'P2BOLD','rejected',3,'fingerprint')`, referrer, referee); err != nil {
		t.Fatal(err)
	}
	rec := callWith(r, tok, "POST", "/v1/referral/apply", `{"code":"P2BREFER"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("antifarm rejection must be permanent, got %d: %s", rec.Code, rec.Body.String())
	}
}

// P3 (было): fingerprint-антифермы в коде не было НИКАКОЙ — 10 tg-аккаунтов с
// одним клиентским fingerprint давали month=30 и 10 бонусных подписок.
//
// Тест идёт через НАСТОЯЩИЙ HTTP apply: снапшот личности берётся только там.
// Проба через сырой INSERT была бы фиктивной — с пустым referee_fp проверка
// корректно не срабатывает, и тест проходил бы, не проверяя ничего.
func TestP3SameFingerprintFarmIsBlocked(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	r, login := testRouter(t)
	svc := New(pg, nil)
	const fp = "SHARED-ATTACKER-FINGERPRINT"
	referrer := mkUser(t, ctx, pg, i64(780001), fp, "10.0.0.1")
	withCode(t, ctx, pg, referrer, "P3SHARED")

	var completed int
	for i := 0; i < 5; i++ {
		referee := mkUser(t, ctx, pg, i64(int64(780100+i)), fp, fmt.Sprintf("10.0.0.%d", i+10))
		tok := login(referee)
		rec := callWithIP(r, tok, "POST", "/v1/referral/apply", `{"code":"P3SHARED"}`, fmt.Sprintf("10.0.0.%d", i+10))
		if rec.Code != http.StatusOK {
			t.Fatalf("referee %d apply: %d %s", i, rec.Code, rec.Body.String())
		}
		// Снапшот обязан быть записан — иначе антиферма не сможет сработать.
		var snapFP, snapIP string
		if err := pg.QueryRow(ctx,
			`SELECT COALESCE(referee_fp,''), COALESCE(referee_ip,'') FROM referrals WHERE referee_id=$1`, referee).
			Scan(&snapFP, &snapIP); err != nil {
			t.Fatal(err)
		}
		if snapFP != fp || snapIP == "" {
			t.Fatalf("referee %d: snapshot not taken at apply (fp=%q ip=%q)", i, snapFP, snapIP)
		}
		svc.CompleteOnFirstReading(ctx, referee)
		st, reason := referralStatus(t, ctx, pg, referee)
		if st == "completed" {
			completed++
			t.Errorf("referee %d completed despite shared fingerprint", i)
		}
		if reason != reasonFingerprint {
			t.Errorf("referee %d: want reason=%q got %q (status=%q)", i, reasonFingerprint, reason, st)
		}
	}
	if completed != 0 {
		t.Fatalf("farm leaked %d completions", completed)
	}
	if m := referrerMonth(t, ctx, pg, referrer); m != 0 {
		t.Fatalf("rejected antifarm must not spend the cap, month=%d", m)
	}
}

// P3c: общий IP — НЕ ловится по умолчанию, и это осознанно.
//
// Проверка по IP выключена дефолтом, потому что IP берётся из X-Real-IP =
// nginx $remote_addr, а set_real_ip_from не настроен: под Cloudflare все
// посетители получат один адрес и проверка отклонит почти всё. Плюс CGNAT.
// Этот тест фиксирует ОБЕ стороны: по умолчанию вектор не ловится, но
// включается одним флагом (см. P3d) и урон всё равно ограничен кэпом.
func TestP3cSameIPIsNotBlockedByDefault(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	svc := New(pg, nil)
	referrer := mkUser(t, ctx, pg, i64(786001), "fp-r", "10.11.0.1")
	referee := mkUser(t, ctx, pg, i64(786002), "fp-different", "10.11.0.1")
	seedPending(t, ctx, pg, referrer, referee, "P3CSIP")
	if _, err := pg.Exec(ctx,
		`UPDATE referrals SET referee_fp='fp-different', referee_ip=$1 WHERE referee_id=$2`,
		hashIP("10.11.0.1"), referee); err != nil {
		t.Fatal(err)
	}
	svc.CompleteOnFirstReading(ctx, referee)
	st, reason := referralStatus(t, ctx, pg, referee)
	if st != "completed" {
		t.Fatalf("antifarm_ip is OFF by default, so same-IP must NOT be rejected: status=%q reason=%q", st, reason)
	}
	if reason != "" {
		t.Fatalf("unexpected reject reason %q", reason)
	}
}

// Тот же вектор при ЯВНО включённом antifarm_ip — обязан ловиться.
func TestP3c2SameIPBlockedWhenAntifarmIPEnabled(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	setReferralCfg(t, ctx, pg, `{"antifarm_ip":true,"antifarm_fp":true}`)
	svc := New(pg, nil)
	referrer := mkUser(t, ctx, pg, i64(786101), "fp-r2", "10.11.0.2")
	referee := mkUser(t, ctx, pg, i64(786102), "fp-different-2", "10.11.0.2")
	seedPending(t, ctx, pg, referrer, referee, "P3CSIP2")
	if _, err := pg.Exec(ctx,
		`UPDATE referrals SET referee_fp='fp-different-2', referee_ip=$1 WHERE referee_id=$2`,
		hashIP("10.11.0.2"), referee); err != nil {
		t.Fatal(err)
	}
	svc.CompleteOnFirstReading(ctx, referee)
	st, reason := referralStatus(t, ctx, pg, referee)
	if st == "completed" {
		t.Fatal("antifarm_ip=true must reject same-IP")
	}
	if reason != reasonIP {
		t.Fatalf("want reason=%q got %q", reasonIP, reason)
	}
}

// Тормоз для CGNAT: antifarm_ip выключается конфигом, без правки кода.
func TestP3dIPCheckCanBeRelaxedByConfig(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	setReferralCfg(t, ctx, pg, `{"antifarm_ip":false,"antifarm_fp":true}`)
	svc := New(pg, nil)
	referrer := mkUser(t, ctx, pg, i64(787001), "fp-r", "10.12.0.1")
	referee := mkUser(t, ctx, pg, i64(787002), "fp-ref", "10.12.0.1")
	seedPending(t, ctx, pg, referrer, referee, "P3DCGNAT")
	if _, err := pg.Exec(ctx,
		`UPDATE referrals SET referee_fp='fp-ref', referee_ip=$1 WHERE referee_id=$2`,
		hashIP("10.12.0.1"), referee); err != nil {
		t.Fatal(err)
	}
	svc.CompleteOnFirstReading(ctx, referee)
	if st, reason := referralStatus(t, ctx, pg, referee); st != "completed" {
		t.Fatalf("antifarm_ip=false must allow CGNAT peers: status=%q reason=%q", st, reason)
	}
}

// P3b: честный сценарий (разные fingerprint и IP) бонус получает.
func TestP3bLegitimateReferralStillGetsBonus(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	svc := New(pg, nil)
	referrer := mkUser(t, ctx, pg, i64(781001), "fp-referrer", "10.1.0.1")
	referee := mkUser(t, ctx, pg, i64(781002), "fp-referee", "10.2.0.9")
	seedPending(t, ctx, pg, referrer, referee, "P3BOK")
	svc.CompleteOnFirstReading(ctx, referee)
	st, reason := referralStatus(t, ctx, pg, referee)
	if st != "completed" {
		t.Fatalf("legit referral rejected: status=%q reason=%q", st, reason)
	}
	if m := referrerMonth(t, ctx, pg, referrer); m != 3 {
		t.Fatalf("referrer month=%d, want 3", m)
	}
}

// P4 (было): pending-строка висела без проверок и при merge переезжала на
// tg-юзера, где и завершалась. Теперь при смене fingerprint отказ reason=identity.
func TestP4IdentitySwapViaMergeIsRejected(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	svc := New(pg, nil)
	referrer := mkUser(t, ctx, pg, i64(782001), "fp-referrer", "10.3.0.1")
	// Снапшот apply от анонима с fingerprint A.
	referee := mkUser(t, ctx, pg, i64(782002), "fp-at-apply", "10.4.0.5")
	seedPending(t, ctx, pg, referrer, referee, "P4SWAP")
	if _, err := pg.Exec(ctx,
		`UPDATE referrals SET referee_fp='fp-at-apply', referee_ip=$1 WHERE referee_id=$2`,
		hashIP("10.4.0.5"), referee); err != nil {
		t.Fatal(err)
	}
	// Merge подменяет личность: fingerprint стал другим (другой браузер/юзер).
	if _, err := pg.Exec(ctx, `UPDATE users SET fingerprint='fp-after-merge' WHERE id=$1`, referee); err != nil {
		t.Fatal(err)
	}
	svc.CompleteOnFirstReading(ctx, referee)
	st, reason := referralStatus(t, ctx, pg, referee)
	if st == "completed" {
		t.Fatal("identity swap completed — merge bypass is back")
	}
	if reason != reasonIdentity {
		t.Fatalf("want reason=%q got %q (status=%q)", reasonIdentity, reason, st)
	}
	if m := referrerMonth(t, ctx, pg, referrer); m != 0 {
		t.Fatalf("rejected identity swap spent the cap: month=%d", m)
	}
}

// P4b: непрерывность личности — это не «мягкий» признак, её нельзя выключить
// конфигом, даже если antifarm_fp/antifarm_ip выключены.
func TestP4bIdentityCheckCannotBeDisabledByConfig(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	setReferralCfg(t, ctx, pg, `{"antifarm_fp":false,"antifarm_ip":false}`)
	referrer := mkUser(t, ctx, pg, i64(783001), "fp-r", "10.5.0.1")
	referee := mkUser(t, ctx, pg, i64(783002), "fp-before", "10.6.0.2")
	seedPending(t, ctx, pg, referrer, referee, "P4BCFG")
	if _, err := pg.Exec(ctx, `UPDATE referrals SET referee_fp='fp-before' WHERE referee_id=$1`, referee); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `UPDATE users SET fingerprint='fp-after' WHERE id=$1`, referee); err != nil {
		t.Fatal(err)
	}
	New(pg, nil).CompleteOnFirstReading(ctx, referee)
	st, reason := referralStatus(t, ctx, pg, referee)
	if st == "completed" {
		t.Fatal("identity swap completed even with antifarm off — continuity must be unconditional")
	}
	if reason != reasonIdentity {
		t.Fatalf("want reason=%q got %q", reasonIdentity, reason)
	}
}

// P5 (было): код disabled/забаненного юзера принимался, бонус капал.
func TestP5DisabledReferrerCodeIsRejected(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	r, login := testRouter(t)
	referrer := testutil.NewUser(t, ctx, pg)
	withCode(t, ctx, pg, referrer, "P5DISABL")
	if _, err := pg.Exec(ctx, `UPDATE users SET status='disabled' WHERE id=$1`, referrer); err != nil {
		t.Fatal(err)
	}
	tok := login(testutil.NewUser(t, ctx, pg))
	rec := callWith(r, tok, "POST", "/v1/referral/apply", `{"code":"P5DISABL"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("disabled referrer code accepted: got %d: %s", rec.Code, rec.Body.String())
	}
}

// P6 (было): lifetime-счётчик рос бесконечно и ни на что не влиял.
func TestP6LifetimeCapStopsBonus(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	setReferralCfg(t, ctx, pg, `{"bonus_days":3,"monthly_cap":30,"lifetime_cap_days":6}`)
	svc := New(pg, nil)
	referrer := mkUser(t, ctx, pg, i64(784001), "fp-cap", "10.7.0.1")
	var completed, lifetimeCapped int
	for i := 0; i < 5; i++ {
		referee := mkUser(t, ctx, pg, i64(int64(784100+i)), fmt.Sprintf("fp-cap-%d", i), fmt.Sprintf("10.8.0.%d", i+1))
		seedPending(t, ctx, pg, referrer, referee, fmt.Sprintf("P6CAP%d", i))
		svc.CompleteOnFirstReading(ctx, referee)
		st, reason := referralStatus(t, ctx, pg, referee)
		switch {
		case st == "completed":
			completed++
		case reason == reasonLifetimeCap:
			lifetimeCapped++
		default:
			t.Fatalf("unexpected outcome %d: status=%q reason=%q", i, st, reason)
		}
	}
	if completed != 2 {
		t.Fatalf("lifetime_cap_days=6 with bonus 3 must allow exactly 2, got %d", completed)
	}
	if lifetimeCapped != 3 {
		t.Fatalf("want 3 lifetime-capped, got %d", lifetimeCapped)
	}
	var lifetime int
	_ = pg.QueryRow(ctx, `SELECT COALESCE(referral_bonus_lifetime,0) FROM entitlements WHERE user_id=$1`, referrer).Scan(&lifetime)
	if lifetime != 6 {
		t.Fatalf("lifetime=%d, want exactly the cap 6", lifetime)
	}
}

// P7 (было): хук fire-and-forget без ретрая — рестарт/деплой оставлял pending
// навсегда, а referee_id UNIQUE не давал применить код заново.
func TestP7ReconcilerCompletesStalePending(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	referrer := testutil.NewUser(t, ctx, pg)
	referee := mkUser(t, ctx, pg, i64(785001), "fp-stale", "10.9.0.1")
	seedPending(t, ctx, pg, referrer, referee, "P7STALE")
	// Читание уже было, и прошло больше pendingStaleAfter — «зависшая» строка.
	if _, err := pg.Exec(ctx, `UPDATE referrals SET created_at = now() - interval '30 minutes' WHERE referee_id=$1`, referee); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx,
		`INSERT INTO readings (user_id, spread_code, question, status, quota_state, seed) VALUES ($1,'daily','q','done','allowed',$2)`,
		referee, int64(42)); err != nil {
		t.Fatal(err)
	}
	n, err := New(pg, nil).ReconcilePending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("reconciler did nothing (n=%d)", n)
	}
	st, _ := referralStatus(t, ctx, pg, referee)
	if st != "completed" {
		t.Fatalf("stale pending not recovered, status=%q", st)
	}
	// Второй проход не должен ничего ломать (идемпотентность).
	if _, err := New(pg, nil).ReconcilePending(ctx); err != nil {
		t.Fatal(err)
	}
	st2, _ := referralStatus(t, ctx, pg, referee)
	if st2 != "completed" {
		t.Fatalf("reconciler is not idempotent, status=%q", st2)
	}
}

// Свежий pending reconciler не трогает: живой хук ещё успеет отработать.
func TestP7bReconcilerIgnoresFreshPending(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	referrer := testutil.NewUser(t, ctx, pg)
	referee := mkUser(t, ctx, pg, i64(785002), "fp-fresh", "10.10.0.1")
	seedPending(t, ctx, pg, referrer, referee, "P7FRESH")
	if _, err := pg.Exec(ctx,
		`INSERT INTO readings (user_id, spread_code, question, status, quota_state, seed) VALUES ($1,'daily','q','done','allowed',$2)`,
		referee, int64(43)); err != nil {
		t.Fatal(err)
	}
	New(pg, nil).ReconcilePending(ctx)
	st, _ := referralStatus(t, ctx, pg, referee)
	if st != "pending" {
		t.Fatalf("fresh pending touched by reconciler: status=%q", st)
	}
}

// P8 (было): месячный ключ считался в зоне процесса, весь остальной продукт —
// в Europe/Moscow. На сервере в UTC месяц переключался на 3 часа раньше.
func TestP8MonthKeyUsesMSK(t *testing.T) {
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Skip("no tzdata")
	}
	// Момент за 3 часа до полуночи МСК 1-го числа: в UTC это ещё прошлый месяц.
	utcLate := time.Date(2026, 10, 1, 0, 30, 0, 0, time.UTC) // 03:30 МСК
	if got := mskMonth(utcLate); got != "2026-10" {
		t.Fatalf("mskMonth(%s) = %q, want 2026-10 (МСК, не локальная зона)", utcLate, got)
	}
	// Обратная сторона: 23:30 МСК 31-го — в UTC уже 1-е число следующего месяца.
	utcEarly := time.Date(2026, 9, 30, 20, 30, 0, 0, time.UTC) // 23:30 МСК 30.09
	if got := mskMonth(utcEarly); got != "2026-09" {
		t.Fatalf("mskMonth(%s) = %q, want 2026-09", utcEarly, got)
	}
	_ = msk
}

// applied_code теперь хранит РЕАЛЬНЫЙ применённый код (аудит), а не случайный
// genCode(): до 037 колонка referrals.code писалась, но не читалась НИ ОДНИМ
// запросом, и расследовать «какой код применил юзер» было нечем.
func TestP9AppliedCodeIsRecordedForAudit(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	r, login := testRouter(t)
	referrer := testutil.NewUser(t, ctx, pg)
	referee := testutil.NewUser(t, ctx, pg)
	withCode(t, ctx, pg, referrer, "P9AUDITC")
	tok := login(referee)
	if rec := callWith(r, tok, "POST", "/v1/referral/apply", `{"code":"P9AUDITC"}`); rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
	}
	var applied, legacy string
	if err := pg.QueryRow(ctx,
		`SELECT COALESCE(applied_code,''), code FROM referrals WHERE referee_id=$1`, referee).
		Scan(&applied, &legacy); err != nil {
		t.Fatal(err)
	}
	if applied != "P9AUDITC" {
		t.Fatalf("applied_code=%q, want the real code P9AUDITC", applied)
	}
	if legacy == "P9AUDITC" {
		t.Fatal("referrals.code must stay a legacy unique row token, not the shared code")
	}
}

// Конфиг реально читается: bonus_days из app_config, а не захардкоженная 3.
func TestP10BonusDaysComeFromConfig(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	setReferralCfg(t, ctx, pg, `{"bonus_days":5,"monthly_cap":40,"lifetime_cap_days":0}`)
	r, login := testRouter(t)
	referrer := mkUser(t, ctx, pg, i64(789001), "fp-cfg-r", "10.13.0.1")
	referee := mkUser(t, ctx, pg, i64(789002), "fp-cfg-ref", "10.13.0.2")
	withCode(t, ctx, pg, referrer, "P10CFG")
	tok := login(referee)
	if rec := callWith(r, tok, "POST", "/v1/referral/apply", `{"code":"P10CFG"}`); rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
	}
	var bonus int
	_ = pg.QueryRow(ctx, `SELECT bonus_days FROM referrals WHERE referee_id=$1`, referee).Scan(&bonus)
	if bonus != 5 {
		t.Fatalf("bonus_days=%d, want 5 from app_config", bonus)
	}
	New(pg, nil).CompleteOnFirstReading(ctx, referee)
	if st, reason := referralStatus(t, ctx, pg, referee); st != "completed" {
		t.Fatalf("status=%q reason=%q", st, reason)
	}
	var days int
	_ = pg.QueryRow(ctx,
		`SELECT EXTRACT(DAY FROM (valid_until - now()))::int FROM subscriptions
		  WHERE user_id=$1 AND plan_code='referral_bonus' ORDER BY created_at DESC LIMIT 1`, referee).Scan(&days)
	if days < 4 || days > 5 {
		t.Fatalf("referee got %d days, want ~5 from config", days)
	}
	if m := referrerMonth(t, ctx, pg, referrer); m != 5 {
		t.Fatalf("referrer month=%d, want 5", m)
	}
}
