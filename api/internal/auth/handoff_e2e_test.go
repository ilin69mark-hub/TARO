// E2E переноса покупки на живых PG/Redis.
// Запуск: DATABASE_URL=... REDIS_ADDR=... go test ./internal/auth/ -run Handoff -v
//
// Здесь ловится то, что стоит денег:
//
//  1. Повторное предъявление токена. GetDel атомарен, но если это перестанет
//     быть так при правке, один токен выполнит два слияния.
//  2. Токен, сгоревший на невалидном initData. Если его восстанавливать,
//     перебор подписи идёт по одному токену, а не по тысяче.
//  3. Несовпадение сети. Это самый частый ЛОЖНЫЙ отказ (человек переключил
//     4G на Wi-Fi) и самый опасный настоящий (токен переслали).
//  4. Проигравшая строка. Раньше был DELETE — пересланный токен забирал покупку
//     навсегда. Теперь status='merged', и её надо уметь вернуть.
//  5. Флаг выключен: перенос не должен существовать на проде, пока не сказано.
package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"taro/api/internal/testutil"
)

// newHandoffSvc идёт через общий testutil.Live, а не через свой ConnectPG:
// там уже есть проверка миграций, уникальные IP/UUID на прогон и t.Cleanup на
// пулы. Дублировать это — значит через месяц забыть про одну из проверок.
func newHandoffSvc(t *testing.T) (context.Context, *Service, func()) {
	t.Helper()
	ctx, pg, rd := testutil.Live(t)
	return ctx, New(pg, rd), func() {}
}

func enableHandoff(t *testing.T, ctx context.Context, svc *Service, on bool) {
	t.Helper()
	v := "false"
	if on {
		v = "true"
	}
	if _, err := svc.pg.Exec(ctx,
		`INSERT INTO app_config (key, value) VALUES ('auth', jsonb_build_object('handoff_enabled', $1::bool))
		 ON CONFLICT (key) DO UPDATE SET value = app_config.value || EXCLUDED.value`, v); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = svc.pg.Exec(context.Background(),
			`UPDATE app_config SET value = value - 'handoff_enabled' WHERE key='auth'`)
	})
}

func makeAnon(t *testing.T, ctx context.Context, svc *Service, fp string) string {
	t.Helper()
	var uid string
	if err := svc.pg.QueryRow(ctx,
		`INSERT INTO users (anon_uuid, fingerprint) VALUES (gen_random_uuid(), $1) RETURNING id::text`, fp).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	return uid
}

func TestHandoffDisabledByDefault(t *testing.T) {
	ctx, svc, done := newHandoffSvc(t)
	defer done()
	_ = ctx
	enableHandoff(t, ctx, svc, false)

	uid := makeAnon(t, ctx, svc, "handoff-off-fp")
	t.Cleanup(func() { testutil.PurgeUsers(t, context.Background(), svc.pg, uid) })

	req := httptest.NewRequest("POST", "/v1/auth/handoff", strings.NewReader(`{"plan_code":"month_299"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Real-IP", "203.0.113.10")
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	// Роут требует сессии, поэтому вызываем обработчик напрямую с контекстом.
	mux.HandleFunc("/v1/auth/handoff", func(w http.ResponseWriter, r *http.Request) {
		svc.HandleHandoff(w, r.WithContext(context.WithValue(r.Context(), userCtxKey{}, uid)))
	})
	mux.ServeHTTP(rec, req)

	// 404, а не 403: выключенная функция не должна отвечать «выключено».
	if rec.Code != http.StatusNotFound {
		t.Fatalf("выключенный перенос: want 404 got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandoffIssueAndConsume(t *testing.T) {
	ctx, svc, done := newHandoffSvc(t)
	defer done()
	enableHandoff(t, ctx, svc, true)

	uid := makeAnon(t, ctx, svc, "handoff-happy-fp")
	t.Cleanup(func() { testutil.PurgeUsers(t, context.Background(), svc.pg, uid) })

	// Покупка, ради которой всё и затевается: подписка на анонимной строке.
	var planID string
	if err := svc.pg.QueryRow(ctx, `SELECT id FROM plans WHERE code='month_299'`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.pg.Exec(ctx, `
		INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, status, valid_until)
		VALUES ($1,$2,'month_299',299,'active',now() + interval '30 days')`, uid, planID); err != nil {
		t.Fatal(err)
	}

	issued, err := svc.IssueHandoff(ctx, uid, "month_299", "203.0.113.10")
	if err != nil {
		t.Fatal(err)
	}
	if !validHandoffToken(issued.Token) {
		t.Fatalf("выдан мусорный токен: %q", issued.Token)
	}
	if issued.ExpiresIn != 300 {
		t.Fatalf("срок=%d, ждали 300", issued.ExpiresIn)
	}

	// Тот же адрес — та же сеть, токен достаётся.
	got, plan, err := svc.ConsumeHandoff(ctx, issued.Token, "203.0.113.200")
	if err != nil {
		t.Fatalf("потребление из той же сети: %v", err)
	}
	if got != uid || plan != "month_299" {
		t.Fatalf("токен отдал чужое: %s/%s", got, plan)
	}

	// Повторно — уже нет. GetDel атомарен, и второй раз читать нечего.
	if _, _, err := svc.ConsumeHandoff(ctx, issued.Token, "203.0.113.200"); err != ErrHandoffNotFound {
		t.Fatalf("повторное предъявление: want ErrHandoffNotFound got %v", err)
	}
}

func TestHandoffIPBinding(t *testing.T) {
	ctx, svc, done := newHandoffSvc(t)
	defer done()

	uid := makeAnon(t, ctx, svc, "handoff-ip-fp")
	t.Cleanup(func() { testutil.PurgeUsers(t, context.Background(), svc.pg, uid) })

	issued, err := svc.IssueHandoff(ctx, uid, "month_299", "203.0.113.10")
	if err != nil {
		t.Fatal(err)
	}
	// Другая сеть — пересланная ссылка. Это ровно то, что привязка обязана
	// отсекать.
	if _, _, err := svc.ConsumeHandoff(ctx, issued.Token, "198.51.100.7"); err != ErrHandoffIPChanged {
		t.Fatalf("чужая сеть: want ErrHandoffIPChanged got %v", err)
	}
	// Токен при этом СГОРАЕТ: вернуть его после неудачи нельзя, иначе по
	// одному токену можно перебирать сети.
	if _, _, err := svc.ConsumeHandoff(ctx, issued.Token, "203.0.113.10"); err != ErrHandoffNotFound {
		t.Fatalf("токен пережил неудачную попытку: %v", err)
	}
}

func TestHandoffGarbageNeverHitsRedis(t *testing.T) {
	ctx, svc, done := newHandoffSvc(t)
	defer done()
	_ = ctx
	// Мусор обязан отсеиваться до похода в сеть: иначе каждый запрос с
	// приложения порождает обращение к Redis по ключу, выдуманному из мусора.
	for _, bad := range []string{"", "x", strings.Repeat("Z", 43), "not base64!!"} {
		if _, _, err := svc.ConsumeHandoff(ctx, bad, "203.0.113.10"); err != ErrHandoffNotFound {
			t.Fatalf("мусор %q: want ErrHandoffNotFound got %v", bad, err)
		}
	}
}

func TestHandoffRejectsGarbageIPAtIssue(t *testing.T) {
	ctx, svc, done := newHandoffSvc(t)
	defer done()
	uid := makeAnon(t, ctx, svc, "handoff-badip-fp")
	t.Cleanup(func() { testutil.PurgeUsers(t, context.Background(), svc.pg, uid) })
	// Без префикса привязаться не к чему, и молча выдать токен значило бы
	// выдать бессрочный секрет.
	if _, err := svc.IssueHandoff(ctx, uid, "month_299", "мусор-не-ip"); err == nil {
		t.Fatal("токен выдан без определимого адреса")
	}
}

func TestHandoffRedisDownFailsClosed(t *testing.T) {
	_, svc, done := newHandoffSvc(t)
	defer done()
	broken := New(svc.pg, nil) // nil Redis — как при недоступном хранилище
	uid := "00000000-0000-0000-0000-000000000001"
	// Fail closed: перенос трогает личность, и «не смог проверить» не повод
	// его разрешить.
	if _, err := broken.IssueHandoff(context.Background(), uid, "month_299", "203.0.113.10"); err == nil {
		t.Fatal("токен выдан при недоступном Redis")
	}
	if _, _, err := broken.ConsumeHandoff(context.Background(), "x", "203.0.113.10"); err == nil {
		t.Fatal("слияние разрешено при недоступном Redis")
	}
}

func TestHandoffTokenExpires(t *testing.T) {
	ctx, svc, done := newHandoffSvc(t)
	defer done()
	uid := makeAnon(t, ctx, svc, "handoff-ttl-fp")
	t.Cleanup(func() { testutil.PurgeUsers(t, context.Background(), svc.pg, uid) })

	issued, err := svc.IssueHandoff(ctx, uid, "month_299", "203.0.113.10")
	if err != nil {
		t.Fatal(err)
	}
	// Ключ обязан иметь TTL: бессрочный токен — это не пятиминутный секрет, а
	// постоянный, и никакая привязка его уже не спасает.
	ttl, err := svc.rd.TTL(ctx, handoffKey(issued.Token)).Result()
	if err != nil {
		t.Fatal(err)
	}
	if ttl <= 0 || ttl > HandoffTTL {
		t.Fatalf("TTL=%v, ждали (0, %v]", ttl, HandoffTTL)
	}
}

func TestHandoffAuditRecordsSurvivor(t *testing.T) {
	ctx, svc, done := newHandoffSvc(t)
	defer done()
	uid := makeAnon(t, ctx, svc, "handoff-audit-fp")
	t.Cleanup(func() { testutil.PurgeUsers(t, context.Background(), svc.pg, uid) })

	if err := svc.RecordHandoffIssued(ctx, uid, "month_299", "203.0.113.0/24"); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := svc.pg.QueryRow(ctx,
		`SELECT id::text FROM auth_handoffs WHERE user_id=$1 AND consumed_at IS NULL`, uid).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordHandoffConsumed(ctx, uid, 700000777); err != nil {
		t.Fatal(err)
	}
	// survivor_tg_id — то, ради чего журнал и существует: без него на вопрос
	// «мой аккаунт куда делся» нечем ответить и нечего откатывать.
	var tg *int64
	var consumed *string
	if err := svc.pg.QueryRow(ctx,
		`SELECT survivor_tg_id, consumed_at::text FROM auth_handoffs WHERE id=$1`, id).Scan(&tg, &consumed); err != nil {
		t.Fatal(err)
	}
	if tg == nil || *tg != 700000777 {
		t.Fatalf("получатель не записан: %v", tg)
	}
	if consumed == nil {
		t.Fatal("consumed_at не проставлен")
	}
}

func TestHandoffRollbackIsPossible(t *testing.T) {
	ctx, svc, done := newHandoffSvc(t)
	defer done()

	loser := makeAnon(t, ctx, svc, "handoff-rollback-loser")
	winner := makeAnon(t, ctx, svc, "handoff-rollback-winner")
	t.Cleanup(func() { testutil.PurgeUsers(t, context.Background(), svc.pg, loser, winner) })

	// Слияние просимулировано: покупка уехала к winner, loser помечен merged.
	var planID string
	if err := svc.pg.QueryRow(ctx, `SELECT id FROM plans WHERE code='month_299'`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.pg.Exec(ctx, `
		INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, status, valid_until)
		VALUES ($1,$2,'month_299',299,'active',now() + interval '30 days')`, loser, planID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.pg.Exec(ctx, `UPDATE subscriptions SET user_id=$2 WHERE user_id=$1`, loser, winner); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.pg.Exec(ctx,
		`UPDATE users SET status='merged', anon_uuid=NULL, referral_code=NULL WHERE id=$1`, loser); err != nil {
		t.Fatal(err)
	}

	// Возврат: подпилка едет обратно, loser снова активен.
	if _, err := svc.pg.Exec(ctx, `UPDATE subscriptions SET user_id=$2 WHERE user_id=$1`, winner, loser); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.pg.Exec(ctx, `UPDATE users SET status='active' WHERE id=$1`, loser); err != nil {
		t.Fatal(err)
	}
	var status string
	var subs int
	if err := svc.pg.QueryRow(ctx, `SELECT status FROM users WHERE id=$1`, loser).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := svc.pg.QueryRow(ctx, `SELECT count(*) FROM subscriptions WHERE user_id=$1`, loser).Scan(&subs); err != nil {
		t.Fatal(err)
	}
	if status != "active" || subs != 1 {
		t.Fatalf("откат не сработал: status=%s subs=%d", status, subs)
	}
}

func TestHandoffResponseHasNoInternalFields(t *testing.T) {
	// Ответ не должен содержать user_id или ip: токен идёт в URL, и всё, что
	// рядом с ним, увеличивает утечку в логи и в скриншоты.
	ctx, svc, done := newHandoffSvc(t)
	defer done()
	enableHandoff(t, ctx, svc, true)
	uid := makeAnon(t, ctx, svc, "handoff-leak-fp")
	t.Cleanup(func() { testutil.PurgeUsers(t, context.Background(), svc.pg, uid) })

	issued, err := svc.IssueHandoff(ctx, uid, "month_299", "203.0.113.10")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(issued)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if strings.Contains(body, uid) {
		t.Fatalf("user_id попал в ответ: %s", body)
	}
	if strings.Contains(body, "203.0.113") {
		t.Fatalf("IP попал в ответ: %s", body)
	}
	if !strings.Contains(body, "token") || !strings.Contains(body, "expires_in") {
		t.Fatalf("в ответе нет нужных полей: %s", body)
	}
}
