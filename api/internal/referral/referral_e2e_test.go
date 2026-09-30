// E2E referral-antifraud против живых PG/Redis (см. D-покрытие, 02-functional/06).
package referral

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/jackc/pgx/v5/pgxpool"
	"taro/api/internal/auth"
	"taro/api/internal/entitlements"

	"taro/api/internal/testutil"
)

func testRouter(t *testing.T) (*chi.Mux, func(uid string) string) {
	t.Helper()
	ctx, pg, rd := testutil.Live(t)
	en := entitlements.New(pg, rd)
	svc := New(pg, en)
	au := auth.New(pg, rd)
	r := chi.NewRouter()
	r.With(au.RequireAuth).Get("/v1/referral/me", svc.HandleMe)
	r.With(au.RequireAuth).Post("/v1/referral/apply", svc.HandleApply)
	login := func(uid string) string {
		t.Helper()
		tok, err := auth.IssueJWT(uid, auth.UserTTL)
		if err != nil {
			t.Fatal(err)
		}
		if err := rd.Set(ctx, "sess:"+uid, "x", auth.UserTTL).Err(); err != nil {
			t.Fatal(err)
		}
		return tok
	}
	return r, login
}

func callWith(r *chi.Mux, tok, method, path, body string) *httptest.ResponseRecorder {
	return callWithIP(r, tok, method, path, body, "")
}

// callWithIP добавляет X-Real-IP — источник адреса для антифермы рефералки.
func callWithIP(r *chi.Mux, tok, method, path, body, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if ip != "" {
		req.Header.Set("X-Real-IP", ip)
	}
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestE2EReferralAntifraud(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	r, login := testRouter(t)

	uidA := testutil.NewUser(t, ctx, pg)
	tokA := login(uidA)
	userA := uidA
	userB := testutil.NewUser(t, ctx, pg)
	tokB := login(userB)

	// A берет код
	rec := callWith(r, tokA, "GET", "/v1/referral/me", "")
	var me map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || me["code"] == nil {
		t.Fatalf("me: %d %s", rec.Code, rec.Body.String())
	}
	code := me["code"].(string)

	// B: self-apply невозможен (свой код) → 409 SELF
	recB := callWith(r, tokB, "GET", "/v1/referral/me", "")
	var meB map[string]any
	_ = json.Unmarshal(recB.Body.Bytes(), &meB)
	recSelf := callWith(r, tokB, "POST", "/v1/referral/apply", `{"code":"`+meB["code"].(string)+`"}`)
	if recSelf.Code != 409 {
		t.Fatalf("self apply: want 409 got %d", recSelf.Code)
	}

	// B применяет код A → pending
	recApply := callWith(r, tokB, "POST", "/v1/referral/apply", `{"code":"`+code+`"}`)
	if recApply.Code != 200 {
		t.Fatalf("apply: want 200 got %d: %s", recApply.Code, recApply.Body.String())
	}
	t.Logf("apply body: %s", recApply.Body.String())

	// Повторный apply → 409 ALREADY_REFERRED. Раньше тут был 200: ON CONFLICT
	// DO NOTHING проглатывал запрос, юзер получал «applied: pending», а
	// привязка оставалась к первому коду. Теперь это 409 по спеке
	// docs/project-book/02-functional/06 — второй код у рефера не принимается.
	recApply2 := callWith(r, tokB, "POST", "/v1/referral/apply", `{"code":"`+code+`"}`)
	if recApply2.Code != http.StatusConflict {
		t.Fatalf("re-apply: want 409 got %d: %s", recApply2.Code, recApply2.Body.String())
	}
	// Считаем ТОЛЬКО строки этого реферала, а не всю таблицу: глобальный
	// COUNT(*) зависит от мусора, оставшегося от других прогонов/пакетов, и
	// ронял `go test -count=2` (A14/F-18). Инвариант, который тут важен:
	// повторный apply не создаёт вторую строку для того же реферера.
	var n int
	_ = pg.QueryRow(ctx,
		`SELECT COUNT(*) FROM referrals WHERE referee_id=$1`, userB).Scan(&n)
	if n != 1 {
		t.Fatalf("want exactly 1 referral row for the referee, got %d", n)
	}
	// Привязка осталась к ПЕРВОМУ коду: 409 не должен молча переписать реферера.
	var boundA int
	_ = pg.QueryRow(ctx,
		`SELECT COUNT(*) FROM referrals WHERE referee_id=$1 AND referrer_id=$2`, userB, userA).Scan(&boundA)
	if boundA != 1 {
		t.Fatalf("409 must not rebind referee, got %d rows to referrer A", boundA)
	}
}

func TestE2ECompleteHook(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	en := entitlements.New(pg, rd)
	svc := New(pg, en)

	// referrer с кодом + referee с tg
	var referrer, referee string
	if err := pg.QueryRow(ctx,
		`INSERT INTO users (anon_uuid) VALUES (gen_random_uuid()) RETURNING id`).Scan(&referrer); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, referrer)
	})
	if err := pg.QueryRow(ctx,
		`INSERT INTO users (tg_id) VALUES (810001) RETURNING id`).Scan(&referee); err != nil {
		_, _ = pg.Exec(ctx, `DELETE FROM users WHERE tg_id=810001`)
		if err := pg.QueryRow(ctx,
			`INSERT INTO users (tg_id) VALUES (810001) RETURNING id`).Scan(&referee); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, referee)
	})
	var code string
	if err := pg.QueryRow(ctx,
		`UPDATE users SET referral_code='HOOKTEST' WHERE id=$1 RETURNING referral_code`, referrer).Scan(&code); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx,
		`INSERT INTO referrals (referrer_id, referee_id, code, status, bonus_days) VALUES ($1,$2,'HOOKTEST','pending',3)`,
		referrer, referee); err != nil {
		t.Fatal(err)
	}
	svc.CompleteOnFirstReading(ctx, referee)
	var st string
	if err := pg.QueryRow(ctx,
		`SELECT status FROM referrals WHERE referrer_id=$1 AND referee_id=$2`, referrer, referee).Scan(&st); err != nil || st != "completed" {
		t.Fatalf("hook: status=%s err=%v", st, err)
	}
	var bonus int
	_ = pg.QueryRow(ctx,
		`SELECT COUNT(*) FROM subscriptions WHERE plan_code='referral_bonus' AND (user_id=$1 OR user_id=$2)`,
		referrer, referee).Scan(&bonus)
	if bonus != 2 {
		t.Fatalf("want 2 bonus subs, got %d", bonus)
	}
	// повторный хук — идемпотентен (pending больше нет)
	svc.CompleteOnFirstReading(ctx, referee)
}

// Аварийный тормоз реферальной программы.
//
// Ключ `referral.enabled` сидился и проходил валидацию, но не читался НИ ОДНОГО
// раза: выключить программу из панели было нельзя, оставался только SQL. При
// обнаружении фермы это худший возможный ответ. Теперь флаг работает, и
// проверяется на живом HTTP-пути, а не только на парсере конфига.
func TestReferralEnabledIsAWorkingBrake(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	r, login := testRouter(t)

	referrer := testutil.NewUser(t, ctx, pg)
	rec := callWith(r, login(referrer), "GET", "/v1/referral/me", "")
	var me map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || me["code"] == nil {
		t.Fatalf("me: %d %s", rec.Code, rec.Body.String())
	}
	code := me["code"].(string)

	// Дефолт: отсутствие ключа = программа работает.
	if raw := referralCfg(t, ctx, pg); !cfgEnabled(raw) {
		t.Fatal("по умолчанию программа должна быть включена")
	}

	restore := testutil.SaveConfigValue(t, ctx, pg, "referral")
	// Мерджим, а не заменяем: в объекте referral живут ещё bonus_days и капы.
	if _, err := pg.Exec(ctx,
		`UPDATE app_config SET value = value || '{"enabled": false}'::jsonb WHERE key='referral'`); err != nil {
		t.Fatal(err)
	}

	referee := testutil.NewUser(t, ctx, pg)
	rec = callWith(r, login(referee), "POST", "/v1/referral/apply", `{"code":"`+code+`"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("при выключенной программе ждём 503, got %d: %s", rec.Code, rec.Body.String())
	}
	// Главное: строка реферала НЕ должна была появиться. Иначе «программа
	// выключена» значило бы только «новым нельзя применить», а бонус всё равно
	// капал бы по дороге.
	var n int
	if err := pg.QueryRow(ctx, `SELECT count(*) FROM referrals WHERE referee_id=$1`, referee).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("при выключенной программе создано рефералов: %d", n)
	}

	// Возврат флага ничего не ломает.
	restore()
	referee2 := testutil.NewUser(t, ctx, pg)
	rec = callWith(r, login(referee2), "POST", "/v1/referral/apply", `{"code":"`+code+`"}`)
	if rec.Code == http.StatusServiceUnavailable {
		t.Fatal("после включения программа обязана принимать код снова")
	}
}

func referralCfg(t *testing.T, ctx context.Context, pg *pgxpool.Pool) []byte {
	t.Helper()
	var raw []byte
	if err := pg.QueryRow(ctx, `SELECT value FROM app_config WHERE key='referral'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func cfgEnabled(raw []byte) bool {
	var doc struct {
		Enabled *bool `json:"enabled"`
	}
	if json.Unmarshal(raw, &doc) != nil || doc.Enabled == nil {
		return true // отсутствие ключа = включено
	}
	return *doc.Enabled
}
