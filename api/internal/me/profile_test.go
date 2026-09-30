// E2E GET /v1/me против живых PG/Redis (compose up db cache).
// Запуск: DATABASE_URL=... REDIS_ADDR=... go test ./internal/me/ -run E2EProfile -v
//
// Что тут ловится. Эндпоинта не было в проекте вообще, и без него у человека
// негде посмотреть свой user_id, а у оператора — негде найти аккаунт для
// выдачи доступа. Добавив его, легко испортить две вещи:
//
//  1. продублировать квоты. free_left/love_left_week/winback считает
//     GET /v1/entitlements/me с кэшем в Redis; если продублировать здесь,
//     получится второй источник правды по лимитам, который разойдётся при
//     первом изменении. Поэтому в профиле квот нет вообще — тест это фиксирует.
//  2. спутать has_payment и «есть подписка». Подписка истекает, факт оплаты —
//     нет. Баннеру «привяжите Telegram» нужен именно факт.
package me

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"taro/api/internal/auth"
	"taro/api/internal/testutil"
)

func TestE2EProfile(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("no DATABASE_URL")
	}
	// Через testutil.Live, а не ConnectPG + defer Close: t.Cleanup выполняется
	// ПОСЛЕ defer, поэтому PurgeUsers в cleanup получал уже закрытый пул
	// ("closed pool") и уборка не происходила. Здесь пул закрывается тоже через
	// t.Cleanup, а зарегистрирован он раньше нашей уборки, значит отработает
	// после неё.
	ctx, pg, rd := testutil.Live(t)

	au := auth.New(pg, rd)
	mv := New(pg, rd)

	var uid string
	if err := pg.QueryRow(ctx,
		`INSERT INTO users (anon_uuid) VALUES (gen_random_uuid()) RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	// PurgeUsers, а не голый DELETE: у юзера появляются подписки и платежи, и
	// FK от них откатывают DELETE целиком — строка остаётся навсегда.
	t.Cleanup(func() { testutil.PurgeUsers(t, context.Background(), pg, uid) })

	tok, err := auth.IssueJWT(uid, auth.UserTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err := rd.Set(ctx, "sess:"+uid, "x", auth.UserTTL).Err(); err != nil {
		t.Fatal(err)
	}

	get := func(cookie string) (int, Profile) {
		req := httptest.NewRequest("GET", "/v1/me", nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: cookie})
		}
		rec := httptest.NewRecorder()
		mux := http.NewServeMux()
		mux.Handle("/v1/me", au.RequireAuth(http.HandlerFunc(mv.HandleGet)))
		mux.ServeHTTP(rec, req)
		var p Profile
		_ = json.NewDecoder(rec.Body).Decode(&p)
		return rec.Code, p
	}

	// Без сессии — 401: профиль это личность, а не публичная карточка.
	if code, _ := get(""); code != 401 {
		t.Fatalf("no cookie: want 401 got %d", code)
	}

	code, p := get(tok)
	if code != 200 {
		t.Fatalf("GET /v1/me: want 200 got %d", code)
	}
	if p.UserID != uid {
		t.Fatalf("user_id: want %s got %s", uid, p.UserID)
	}
	// Аноним: Telegram не привязан, платежей не было.
	if p.TelegramLinked {
		t.Fatal("telegram_linked must be false for anon")
	}
	if p.HasPayment {
		t.Fatal("has_payment must be false without payments")
	}
	if p.AgeConfirmed {
		t.Fatal("age_confirmed must be false before POST /v1/me/age")
	}
	if p.ValidUntil != nil {
		t.Fatal("valid_until must be nil without subscription")
	}
	// Квот в профиле быть не должно — они у /v1/entitlements/me.
	var raw map[string]any
	req := httptest.NewRequest("GET", "/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	mux.Handle("/v1/me", au.RequireAuth(http.HandlerFunc(mv.HandleGet)))
	mux.ServeHTTP(rec, req)
	_ = json.NewDecoder(rec.Body).Decode(&raw)
	for _, k := range []string{"free_left", "love_left_week", "winback"} {
		if _, ok := raw[k]; ok {
			t.Fatalf("profile must not duplicate entitlement %q — второй источник правды по лимитам", k)
		}
	}

	// Истёкшая подписка НЕ должна превращать has_payment в false: человек
	// платил, и предложение «купить» ему показывать нельзя.
	// Колонки берём по схеме: у subscriptions plan_id NOT NULL.
	var planID string
	if err := pg.QueryRow(ctx, `SELECT id FROM plans WHERE code='month_299'`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(-24 * time.Hour)
	if _, err := pg.Exec(ctx, `
		INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, status, valid_until)
		VALUES ($1,$2,'month_299',299,'expired',$3)`, uid, planID, until); err != nil {
		t.Fatal(err)
	}
	// purchase_fingerprint не передаём: триггер payments_snapshot_guard сам
	// считает его из plan_code|price|stars, и подсованная строка отвергается.
	if _, err := pg.Exec(ctx, `
		INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider,
		                      provider_payment_id, amount_rub, status)
		VALUES ($1,$2,'month_299',299,'tg_stars',$3,299,'succeeded')`,
		uid, planID, "e2e-prof-"+uid); err != nil {
		t.Fatal(err)
	}
	_, p = get(tok)
	if !p.HasPayment {
		t.Fatal("has_payment must be true after a succeeded payment even if the subscription expired")
	}
	if p.ValidUntil != nil {
		t.Fatal("valid_until must be nil for an expired subscription")
	}
	if p.TelegramLinked {
		t.Fatal("telegram_linked must stay false")
	}

	// Привязка Telegram переключает флаг — на нём и построен баннер.
	//
	// tg_id уникален по всей таблице, а idx_users_tg — глобальный индекс. Раньше
	// здесь брался ПЕРВЫЙ значащий знак uid, то есть всего 10 возможных
	// значений на весь мир. Как только несколько прогонов не убрали за собой
	// строки (а `DELETE FROM users` молча откатывался на FK от подписок и
	// платежей), тест начинал падать на duplicate key по чужой строке — не на
	// своей логике. Теперь 8 знаков uid плюс соль с time.Now: диапазон ~10^10,
	// коллизия практически исключена. Уборка — testutil.PurgeUsers в
	// t.Cleanup ниже.
	derived := int64(7000000000)
	for i, b := range []byte(uid) {
		if b >= '0' && b <= '9' {
			derived = derived*10 + int64(b-'0')
			if i >= 7 {
				break
			}
		}
	}
	if _, err := pg.Exec(ctx, `UPDATE users SET tg_id=$2 WHERE id=$1`, uid, derived); err != nil {
		t.Fatal(err)
	}
	_, p = get(tok)
	if !p.TelegramLinked {
		t.Fatal("telegram_linked must be true after tg_id is set")
	}
}
