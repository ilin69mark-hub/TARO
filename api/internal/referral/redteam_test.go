// Red-team прогон: исходные 8 обходов (аудит P1–P8) против уже закрытых защит.
//
// Каждая атака выполняется через НАСТОЯЩИЙ HTTP-поверхности (chi-роутер с
// реальной аутентификацией), а не прямыми INSERT: иначе снапшот личности не
// берётся и тест проверял бы пустоту. У каждой атаки есть ВЕРДИКТ
// (заблокирована/пропустила) и ожидание — тест падает, если хоть одна атака
// снова начнёт проходить.
package referral

import (
	"fmt"
	"net/http"
	"testing"

	"taro/api/internal/testutil"
)

// verdict — результат одной атаки.
//
// Три состояния, а не два. «ОГРАНИЧЕНО» — честный ответ для векторов, которые
// принципиально не ловятся без ложных срабатываний у честных пользователей
// (CGNAT). Там ущерб ограничен месячным кэпом реферера, и это надо называть
// своим именем, а не выдавать за «заблокировано».
type verdict struct {
	name   string
	state  string // "BLOCKED" | "CAPPED" | "LEAKED"
	detail string
}

const (
	stBlocked = "BLOCKED"
	stCapped  = "CAPPED"
	stLeaked  = "LEAKED"
)

func (v verdict) String() string {
	switch v.state {
	case stBlocked:
		return fmt.Sprintf("  ЗАБЛОКИРОВАНО  %-46s", v.name)
	case stCapped:
		return fmt.Sprintf("  ОГРАНИЧЕНО КЭПОМ  %-36s  (%s)", v.name, v.detail)
	default:
		return fmt.Sprintf("  ПРОПУСТИЛА!!!  %-46s  (%s)", v.name, v.detail)
	}
}

// farmN — размер фермы: столько tg-аккаунтов атакующий поднимает за раз.
const farmN = 10

// TestRedTeamOriginalAttacks — полный повтор исходного аудита.
func TestRedTeamOriginalAttacks(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	r, login := testRouter(t)
	svc := New(pg, nil)
	var out []verdict
	add := func(name, state, detail string) {
		out = append(out, verdict{name, state, detail})
	}

	// ── Атака 1: 10 tg-аккаунтов из ОДНОГО браузера, реферер — жертва ───────
	//
	// Именно так выглядит реальная ферма: реферальный код ЧУЖОЙ (реферер не
	// участвует), а бонусы (по 3 дня на аккаунт) получает атакующий. Раньше
	// парная проверка referrer_fp==referee_fp это не ловила: fingerprint
	// реферера другой, совпадения нет. Ловит кластерная проверка.
	const fpFarm = "REDTEAM-SHARED-FINGERPRINT"
	victim := mkUser(t, ctx, pg, i64(990001), "fp-legit-victim", "203.0.113.1")
	withCode(t, ctx, pg, victim, "RTMAIN001")
	leaked := 0
	for i := 0; i < farmN; i++ {
		referee := mkUser(t, ctx, pg, i64(int64(990100+i)), fpFarm, fmt.Sprintf("198.51.100.%d", i+1))
		tok := login(referee)
		rec := callWithIP(r, tok, "POST", "/v1/referral/apply", `{"code":"RTMAIN001"}`,
			fmt.Sprintf("198.51.100.%d", i+1))
		if rec.Code != http.StatusOK {
			t.Fatalf("attack 1 setup: apply %d %s", rec.Code, rec.Body.String())
		}
		svc.CompleteOnFirstReading(ctx, referee)
		if st, _ := referralStatus(t, ctx, pg, referee); st == "completed" {
			leaked++
		}
	}
	month := referrerMonth(t, ctx, pg, victim)
	// Кластерный порог НЕ ловит ферму полностью — он её УЖИМАЕТ до
	// fpClusterLimit аккаунтов на браузер. Отличить 3 человека за одним
	// ноутбуком от 3 аккаунтов фермы на сервере невозможно, поэтому состояние
	// честно «ограничено», а не «заблокировано».
	switch {
	case leaked == 0:
		add("ферма: чужой код + 10 аккаунтов из 1 браузера", stBlocked, "")
	case leaked <= defaultFPClusterLimit && month <= defaultMonthlyCapDays:
		add("ферма: чужой код + 10 аккаунтов из 1 браузера", stCapped,
			fmt.Sprintf("%d из 10 прошли (порог %d), жертва отдала %d из %d дней",
				leaked, defaultFPClusterLimit, month, defaultMonthlyCapDays))
	default:
		add("ферма: чужой код + 10 аккаунтов из 1 браузера", stLeaked,
			fmt.Sprintf("completed=%d, жертва отдала month=%d дней", leaked, month))
	}

	// ── Атака 2: 10 tg-аккаунтов с ОДНОГО IP, реферер — жертва ──────────────
	victim2 := mkUser(t, ctx, pg, i64(990200), "fp-legit-victim-2", "203.0.113.2")
	withCode(t, ctx, pg, victim2, "RTMAIN002")
	leaked = 0
	for i := 0; i < farmN; i++ {
		// Разные fingerprint (чистый браузер на каждый аккаунт), один IP.
		referee := mkUser(t, ctx, pg, i64(int64(990250+i)), fmt.Sprintf("fp-clean-%d", i), "192.0.2.77")
		tok := login(referee)
		rec := callWithIP(r, tok, "POST", "/v1/referral/apply", `{"code":"RTMAIN002"}`, "192.0.2.77")
		if rec.Code != http.StatusOK {
			t.Fatalf("attack 2 setup: apply %d %s", rec.Code, rec.Body.String())
		}
		svc.CompleteOnFirstReading(ctx, referee)
		if st, _ := referralStatus(t, ctx, pg, referee); st == "completed" {
			leaked++
		}
	}
	month2 := referrerMonth(t, ctx, pg, victim2)
	// Этот вектор по умолчанию НЕ ловится (antifarm_ip=false — требование к
	// окружению, см. миграцию 039): 10 аккаунтов с чистыми
	// fingerprint'ами и одного IP неотличимы от семьи/офиса за общим
	// мобильным CGNAT. Ловится только включением ip_cluster_limit — с
	// ложными отказами у честных, поэтому выключено. Ущерб ограничен
	// месячным кэпом жертвы: 30 дней, независимо от числа аккаунтов.
	switch {
	case leaked == 0:
		add("ферма: чужой код + 10 аккаунтов с 1 IP", stBlocked, "")
	case month2 <= defaultMonthlyCapDays:
		add("ферма: чужой код + 10 аккаунтов с 1 IP", stCapped,
			fmt.Sprintf("completed=%d, но жертва отдала %d из %d дней кэпа",
				leaked, month2, defaultMonthlyCapDays))
	default:
		add("ферма: чужой код + 10 аккаунтов с 1 IP", stLeaked,
			fmt.Sprintf("completed=%d, жертва отдала month=%d БЕЗ КЭПА", leaked, month2))
	}

	// ── Атака 3: подмена личности через merge (смена fingerprint) ────────────
	ref3 := mkUser(t, ctx, pg, i64(990300), "fp-r3", "203.0.113.3")
	withCode(t, ctx, pg, ref3, "RTMAIN003")
	ref3e := mkUser(t, ctx, pg, i64(990301), "fp-before-merge", "198.51.100.200")
	tok3 := login(ref3e)
	if rec := callWithIP(r, tok3, "POST", "/v1/referral/apply", `{"code":"RTMAIN003"}`, "198.51.100.200"); rec.Code != http.StatusOK {
		t.Fatalf("attack 3 setup: %d %s", rec.Code, rec.Body.String())
	}
	// merge подменяет личность: браузер рефера сменился.
	if _, err := pg.Exec(ctx, `UPDATE users SET fingerprint='fp-after-merge' WHERE id=$1`, ref3e); err != nil {
		t.Fatal(err)
	}
	svc.CompleteOnFirstReading(ctx, ref3e)
	st3, reason3 := referralStatus(t, ctx, pg, ref3e)
	if st3 != "completed" && reason3 == reasonIdentity {
		add("merge: подмена fingerprint рефера (через pending)", stBlocked, "")
	} else {
		add("merge: подмена fingerprint рефера (через pending)", stLeaked, fmt.Sprintf("status=%q reason=%q", st3, reason3))
	}

	// ── Атака 4: второй код вместо первого (тихий 200) ───────────────────────
	ref4a := testutil.NewUser(t, ctx, pg)
	ref4b := testutil.NewUser(t, ctx, pg)
	withCode(t, ctx, pg, ref4a, "RTMAIN04A")
	withCode(t, ctx, pg, ref4b, "RTMAIN04B")
	ref4e := testutil.NewUser(t, ctx, pg)
	tok4 := login(ref4e)
	callWithIP(r, tok4, "POST", "/v1/referral/apply", `{"code":"RTMAIN04B"}`, "198.51.100.50")
	rec4 := callWithIP(r, tok4, "POST", "/v1/referral/apply", `{"code":"RTMAIN04A"}`, "198.51.100.50")
	var rebound int
	_ = pg.QueryRow(ctx, `SELECT COUNT(*) FROM referrals WHERE referee_id=$1 AND referrer_id=$2`, ref4e, ref4a).Scan(&rebound)
	if rec4.Code == http.StatusConflict && rebound == 0 {
		add("второй код: тихий 200 + перепривязка", stBlocked, "")
	} else {
		add("второй код: тихий 200 + перепривязка", stLeaked, fmt.Sprintf("HTTP=%d, перепривязка=%d", rec4.Code, rebound))
	}

	// ── Атака 5: код забаненного реферера ────────────────────────────────────
	ref5 := testutil.NewUser(t, ctx, pg)
	withCode(t, ctx, pg, ref5, "RTMAIN005")
	if _, err := pg.Exec(ctx, `UPDATE users SET status='disabled' WHERE id=$1`, ref5); err != nil {
		t.Fatal(err)
	}
	tok5 := login(testutil.NewUser(t, ctx, pg))
	rec5 := callWithIP(r, tok5, "POST", "/v1/referral/apply", `{"code":"RTMAIN005"}`, "198.51.100.60")
	if rec5.Code == http.StatusUnprocessableEntity {
		add("код забаненного (status=disabled) реферера", stBlocked, "")
	} else {
		add("код забаненного (status=disabled) реферера", stLeaked, fmt.Sprintf("HTTP=%d", rec5.Code))
	}

	// ── Атака 6: lifetime-кап (обход месячного через N месяцев) ──────────────
	setReferralCfg(t, ctx, pg, `{"bonus_days":3,"monthly_cap":30,"lifetime_cap_days":9}`)
	ref6 := mkUser(t, ctx, pg, i64(990600), "fp-r6", "203.0.113.6")
	withCode(t, ctx, pg, ref6, "RTMAIN006")
	var completed6, capped6 int
	for i := 0; i < 10; i++ {
		referee := mkUser(t, ctx, pg, i64(int64(990650+i)), fmt.Sprintf("fp-6-%d", i), fmt.Sprintf("198.51.101.%d", i+1))
		tok := login(referee)
		if rec := callWithIP(r, tok, "POST", "/v1/referral/apply", `{"code":"RTMAIN006"}`,
			fmt.Sprintf("198.51.101.%d", i+1)); rec.Code != http.StatusOK {
			t.Fatalf("attack 6 setup: %d %s", rec.Code, rec.Body.String())
		}
		svc.CompleteOnFirstReading(ctx, referee)
		st, reason := referralStatus(t, ctx, pg, referee)
		if st == "completed" {
			completed6++
		}
		if reason == reasonLifetimeCap {
			capped6++
		}
	}
	var lifetime int
	_ = pg.QueryRow(ctx, `SELECT COALESCE(referral_bonus_lifetime,0) FROM entitlements WHERE user_id=$1`, ref6).Scan(&lifetime)
	if completed6 == 3 && lifetime == 9 {
		add("lifetime-кап: месячный обход за 3 месяца", stBlocked, "")
	} else {
		add("lifetime-кап: месячный обход за 3 месяца", stLeaked, fmt.Sprintf("completed=%d (ждём 3), lifetime=%d (потолок 9)", completed6, lifetime))
	}

	// ── Атака 7: зависший pending (эмуляция рестарта/деплоя) ────────────────
	ref7 := mkUser(t, ctx, pg, i64(990700), "fp-r7", "203.0.113.7")
	withCode(t, ctx, pg, ref7, "RTMAIN007")
	ref7e := mkUser(t, ctx, pg, i64(990701), "fp-r7-ref", "198.51.100.70")
	tok7 := login(ref7e)
	if rec := callWithIP(r, tok7, "POST", "/v1/referral/apply", `{"code":"RTMAIN007"}`, "198.51.100.70"); rec.Code != http.StatusOK {
		t.Fatalf("attack 7 setup: %d %s", rec.Code, rec.Body.String())
	}
	// Хук не отработал (рестарт), но чтение уже есть.
	if _, err := pg.Exec(ctx,
		`INSERT INTO readings (user_id, spread_code, question, status, quota_state, seed) VALUES ($1,'daily','q','done','allowed',$2)`,
		ref7e, int64(7)); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `UPDATE referrals SET created_at=now()-interval '30 minutes' WHERE referee_id=$1`, ref7e); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReconcilePending(ctx); err != nil {
		t.Fatal(err)
	}
	st7, _ := referralStatus(t, ctx, pg, ref7e)
	if st7 == "completed" {
		add("зависший pending: хук потерян на рестарте", stBlocked, "")
	} else {
		add("зависший pending: хук потерян на рестарте", stLeaked, fmt.Sprintf("после reconciler status=%q", st7))
	}

	// ── Атака 8: переигровка отказа по антиферме (перебор) ───────────────────
	// Здесь реферер и рефёр ДЕЛЯТ fingerprint — иначе антиферма не сработает и
	// отказа не будет, а значит и переигрывать будет нечего.
	ref8 := mkUser(t, ctx, pg, i64(990800), fpFarm, "203.0.113.8")
	withCode(t, ctx, pg, ref8, "RTMAIN008")
	ref8e := mkUser(t, ctx, pg, i64(990801), fpFarm, "198.51.100.80")
	tok8 := login(ref8e)
	if rec := callWithIP(r, tok8, "POST", "/v1/referral/apply", `{"code":"RTMAIN008"}`, "198.51.100.80"); rec.Code != http.StatusOK {
		t.Fatalf("attack 8 setup: %d %s", rec.Code, rec.Body.String())
	}
	svc.CompleteOnFirstReading(ctx, ref8e)
	st8, reason8 := referralStatus(t, ctx, pg, ref8e)
	// Отказ по антиферме обязан быть постоянным: попытка переиграть его = перебор.
	replay := callWithIP(r, tok8, "POST", "/v1/referral/apply", `{"code":"RTMAIN008"}`, "198.51.100.80")
	if st8 != "completed" && replay.Code == http.StatusConflict {
		add("переигровка отказа по антиферме (перебор)", stBlocked, "")
	} else {
		add("переигровка отказа по антиферме (перебор)", stLeaked,
			fmt.Sprintf("status=%q reason=%q, replay HTTP=%d", st8, reason8, replay.Code))
	}

	// ── Контроль: честный сценарий всё ещё работает ─────────────────────────
	refOk := mkUser(t, ctx, pg, i64(990900), "fp-ok-ref", "203.0.113.9")
	withCode(t, ctx, pg, refOk, "RTMAIN009")
	refOkE := mkUser(t, ctx, pg, i64(990901), "fp-ok-new", "198.51.100.90")
	tokOk := login(refOkE)
	if rec := callWithIP(r, tokOk, "POST", "/v1/referral/apply", `{"code":"RTMAIN009"}`, "198.51.100.90"); rec.Code != http.StatusOK {
		t.Fatalf("legit setup: %d %s", rec.Code, rec.Body.String())
	}
	svc.CompleteOnFirstReading(ctx, refOkE)
	stOk, reasonOk := referralStatus(t, ctx, pg, refOkE)
	monthOk := referrerMonth(t, ctx, pg, refOk)
	if stOk == "completed" && monthOk > 0 {
		add("КОНТРОЛЬ: честный реферал получает бонус", stBlocked, "")
	} else {
		add("КОНТРОЛЬ: честный реферал получает бонус", stLeaked, fmt.Sprintf("status=%q reason=%q month=%d", stOk, reasonOk, monthOk))
	}

	t.Log("\n=== RED-TEAM: повтор исходных атак ===")
	for _, v := range out {
		t.Log(v.String())
	}
	for i, v := range out {
		if v.state == stLeaked {
			t.Errorf("атака %d пройдена: %s — %s", i+1, v.name, v.detail)
		}
	}
}

// Контроль честности: антиферма не должна превратиться в «никто ничего не
// получает». Разные люди с разных устройств — бонус получают.
func TestRedTeamLegitimateTrafficUnaffected(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	r, login := testRouter(t)
	svc := New(pg, nil)
	referrer := mkUser(t, ctx, pg, i64(991001), "fp-legit-r", "203.0.113.100")
	withCode(t, ctx, pg, referrer, "RTLEGIT01")

	const friends = 5
	for i := 0; i < friends; i++ {
		referee := mkUser(t, ctx, pg, i64(int64(991100+i)),
			fmt.Sprintf("fp-legit-%d", i), fmt.Sprintf("198.51.100.%d", 200+i))
		tok := login(referee)
		if rec := callWithIP(r, tok, "POST", "/v1/referral/apply", `{"code":"RTLEGIT01"}`,
			fmt.Sprintf("198.51.100.%d", 200+i)); rec.Code != http.StatusOK {
			t.Fatalf("legit apply %d: %d %s", i, rec.Code, rec.Body.String())
		}
		svc.CompleteOnFirstReading(ctx, referee)
		st, reason := referralStatus(t, ctx, pg, referee)
		if st != "completed" {
			t.Errorf("честный друг %d не получил бонус: status=%q reason=%q", i, st, reason)
		}
	}
	if m := referrerMonth(t, ctx, pg, referrer); m != friends*3 {
		t.Fatalf("referrer month=%d, want %d", m, friends*3)
	}
	t.Logf("5 честных друзей получили бонус, referrer month=%d (кап 30 не задет)", referrerMonth(t, ctx, pg, referrer))
}

// TestRedTeamClusterCheckIsWhatBoundsTheFarm — контрольный эксперимент: та же
// ферма, но с ВЫКЛЮЧЕННЫМ кластерным порогом.
//
// Показывает цену настройки: без порога ферма проходит целиком, и единственное,
// что её останавливает — месячный кэп жертвы. То есть fpClusterLimit реально
// работает, а не декоративен.
func TestRedTeamClusterCheckIsWhatBoundsTheFarm(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	r, login := testRouter(t)
	svc := New(pg, nil)
	setReferralCfg(t, ctx, pg, `{"bonus_days":3,"monthly_cap":30,"lifetime_cap_days":0,"fingerprint_cluster_limit":0,"antifarm_ip":false}`)
	const fpFarm = "REDTEAM-CLUSTER-OFF"
	victim := mkUser(t, ctx, pg, i64(992001), "fp-victim-cluster", "203.0.113.30")
	withCode(t, ctx, pg, victim, "RTCLUST01")

	var completed int
	for i := 0; i < farmN; i++ {
		referee := mkUser(t, ctx, pg, i64(int64(992100+i)), fpFarm, fmt.Sprintf("198.51.100.%d", i+30))
		tok := login(referee)
		rec := callWithIP(r, tok, "POST", "/v1/referral/apply", `{"code":"RTCLUST01"}`,
			fmt.Sprintf("198.51.100.%d", i+30))
		if rec.Code != http.StatusOK {
			t.Fatalf("apply %d: %d %s", i, rec.Code, rec.Body.String())
		}
		svc.CompleteOnFirstReading(ctx, referee)
		if st, _ := referralStatus(t, ctx, pg, referee); st == "completed" {
			completed++
		}
	}
	month := referrerMonth(t, ctx, pg, victim)
	t.Logf("КЛАСТЕР ВЫКЛЮЧЕН: completed=%d из %d, жертва отдала month=%d дней (кэп %d)",
		completed, farmN, month, defaultMonthlyCapDays)
	if completed != farmN {
		t.Errorf("без кластерного порога ферма должна проходить целиком, прошло %d", completed)
	}
	// Единственное, что удержало ущерб, — месячный кэп.
	if month > defaultMonthlyCapDays {
		t.Errorf("месячный кэп пробит: month=%d > %d", month, defaultMonthlyCapDays)
	}
}

// TestRedTeamIPClusterCanBeEnabled — тот же вектор (один IP), но с включённым
// ip_cluster_limit. Показывает, что ловушка существует и включается конфигом,
// вместе с её ценой (ложные отказы у честных за CGNAT).
func TestRedTeamIPClusterCanBeEnabled(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	r, login := testRouter(t)
	svc := New(pg, nil)
	setReferralCfg(t, ctx, pg, `{"bonus_days":3,"monthly_cap":30,"lifetime_cap_days":0,"fingerprint_cluster_limit":0,"ip_cluster_limit":4}`)
	victim := mkUser(t, ctx, pg, i64(993001), "fp-victim-ip", "203.0.113.40")
	withCode(t, ctx, pg, victim, "RTIPCL01")

	var completed int
	for i := 0; i < farmN; i++ {
		referee := mkUser(t, ctx, pg, i64(int64(993100+i)), fmt.Sprintf("fp-ip-clean-%d", i), "192.0.2.88")
		tok := login(referee)
		rec := callWithIP(r, tok, "POST", "/v1/referral/apply", `{"code":"RTIPCL01"}`, "192.0.2.88")
		if rec.Code != http.StatusOK {
			t.Fatalf("apply %d: %d %s", i, rec.Code, rec.Body.String())
		}
		svc.CompleteOnFirstReading(ctx, referee)
		if st, _ := referralStatus(t, ctx, pg, referee); st == "completed" {
			completed++
		}
	}
	t.Logf("IP-КЛАСТЕР ВКЛЮЧЁН (лимит 4): completed=%d из %d", completed, farmN)
	if completed >= 4 {
		t.Errorf("ip_cluster_limit=4 должен резать ферму, прошло %d", completed)
	}
}
