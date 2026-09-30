// Порядок операций в /v1/auth/link при переносе.
//
// Обе проверки ниже выросли из ЖИВОГО прогона, а не из рассуждения. Это
// ровно тот случай, когда тест на уровне сервиса был зелёным, а человек
// получал отказ: unit-тесты звали ConsumeHandoff и Link() напрямую и не
// проходили через HandleLink, где и живёт порядок.
//
//  1. Битый initData НЕ ДОЛЖЕН сжигать одноразовый токен. WebApp не всегда
//     успевает инициализироваться, и с обратным порядком человек терял
//     перенос и должен был начинать заново из браузера.
//  2. Пустая сессия в WebView — это НОРМА, а не 401. WebView никогда не видел
//     cookie анонимного покупателя, так что требовать вход значило бы запретить
//     перенос полностью.
package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"taro/api/internal/testutil"
)

// linkHandoffLive — один вызов HandleLink с токеном переноса, от имени того, у
// кого в WebView нет сессии (то есть у всех).
func linkHandoffLive(t *testing.T, svc *Service, body, ip string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/auth/link", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Real-IP", ip)
	rec := httptest.NewRecorder()
	svc.HandleLink(rec, req)
	return rec
}

func TestLinkHandoffBadInitDataKeepsToken(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-0123456789abcdef0123456789")
	t.Setenv("TG_BOT_TOKEN", "test-bot")
	t.Setenv("TG_ALLOW_DEV_BOT", "1")
	ctx, pg, rd, svc := liveAuth(t)
	_ = rd
	enableHandoff(t, ctx, svc, true)
	uid := makeAnon(t, ctx, svc, "order-b-init-fp")
	t.Cleanup(func() { testutil.PurgeUsers(t, context.Background(), pg, uid) })

	issued, err := svc.IssueHandoff(ctx, uid, "month_299", "203.0.113.10")
	if err != nil {
		t.Fatal(err)
	}
	// Подпись заведомо неверная: VerifyInitData обязан отбить.
	rec := linkHandoffLive(t, svc, `{"initData":"auth_date=1&hash=deadbeef","handoff":"`+issued.Token+`"}`, "203.0.113.10")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("битый initData: want 401 got %d: %s", rec.Code, rec.Body.String())
	}
	// Главное: токен обязан выжить. Сожжённый токен означал бы, что человек
	// начинает перенос заново из-за того, что WebApp не успел отдать initData.
	if _, _, err := svc.ConsumeHandoff(ctx, issued.Token, "203.0.113.10"); err != nil {
		t.Fatalf("токен сгорел на битом initData: %v", err)
	}
}

func TestLinkHandoffWorksWithoutBrowserSession(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-0123456789abcdef0123456789")
	t.Setenv("TG_BOT_TOKEN", "test-bot")
	t.Setenv("TG_ALLOW_DEV_BOT", "1")
	ctx, pg, rd, svc := liveAuth(t)
	_ = rd
	enableHandoff(t, ctx, svc, true)
	payer := makeAnon(t, ctx, svc, "order-nosess-fp")

	// Telegram-аккаунт, в который переносим.
	tgID := int64(799321001)
	var winner string
	if err := pg.QueryRow(ctx,
		`INSERT INTO users (tg_id, fingerprint) VALUES ($1,'nosess-win-fp') RETURNING id::text`, tgID).Scan(&winner); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.PurgeUsers(t, context.Background(), pg, payer, winner) })

	issued, err := svc.IssueHandoff(ctx, payer, "month_299", "203.0.113.10")
	if err != nil {
		t.Fatal(err)
	}
	// Запрос БЕЗ cookie: ровно так приходит из WebView. Раньше здесь стоял
	// 401, который запрещал перенос целиком.
	init := craft(t, "test-bot", tgID)
	rec := linkHandoffLive(t, svc,
		`{"initData":"`+init+`","handoff":"`+issued.Token+`"}`, "203.0.113.10")
	if rec.Code != http.StatusOK {
		t.Fatalf("перенос без браузерной сессии: want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	var status string
	if err := pg.QueryRow(ctx, `SELECT status FROM users WHERE id=$1`, payer).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "merged" {
		t.Fatalf("статус плательщика после переноса: %s", status)
	}
}

func TestLinkHandoffSurvivesTelegramUserWithOwnSession(t *testing.T) {
	// Человек уже привязан к Telegram, потом заплатил анонимно с нового
	// устройства и переносит. В WebView у него ЕСТЬ сессия — старый аккаунт.
	// Сверка «сессия == токен» отбила бы такой перенос с неверным
	// HANDOFF_IP_MISMATCH, поэтому её быть не должно.
	t.Setenv("JWT_SECRET", "test-secret-0123456789abcdef0123456789")
	t.Setenv("TG_BOT_TOKEN", "test-bot")
	t.Setenv("TG_ALLOW_DEV_BOT", "1")
	ctx, pg, rd, svc := liveAuth(t)
	_ = rd
	enableHandoff(t, ctx, svc, true)
	payer := makeAnon(t, ctx, svc, "order-ownold-fp")

	tgID := int64(799321002)
	var winner string
	if err := pg.QueryRow(ctx,
		`INSERT INTO users (tg_id, fingerprint) VALUES ($1,'ownold-win-fp') RETURNING id::text`, tgID).Scan(&winner); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.PurgeUsers(t, context.Background(), pg, payer, winner) })

	issued, err := svc.IssueHandoff(ctx, payer, "month_299", "203.0.113.10")
	if err != nil {
		t.Fatal(err)
	}
	init := craft(t, "test-bot", tgID)

	// Запрос с cookie ЧУЖОГО аккаунта (как у человека с историей в WebView).
	tok, err := IssueJWT(winner, UserTTL)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/auth/link",
		strings.NewReader(`{"initData":"`+init+`","handoff":"`+issued.Token+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Real-IP", "203.0.113.10")
	req.AddCookie(&http.Cookie{Name: CookieName, Value: tok})
	req = req.WithContext(context.WithValue(req.Context(), userCtxKey{}, winner))
	rec := httptest.NewRecorder()
	svc.HandleLink(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("перенос при наличии чужой сессии: want 200 got %d: %s", rec.Code, rec.Body.String())
	}
}
