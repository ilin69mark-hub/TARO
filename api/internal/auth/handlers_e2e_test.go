// E2E auth-хендлеры: anon/telegram/link/refresh через chi-роутер (см. D-покрытие).
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/jackc/pgx/v5/pgxpool"

	"taro/api/internal/testutil"
)

func authRouter(t *testing.T) (*chi.Mux, *Service) {
	r, svc, _ := authRouterPG(t)
	return r, svc
}

// authRouterPG — тот же роутер, но с пулом наружу: тестам, которые создают
// юзеров, нужна уборка, а уборке нужен доступ к БД.
func authRouterPG(t *testing.T) (*chi.Mux, *Service, *pgxpool.Pool) {
	t.Helper()
	_, pg, rd := testutil.Live(t)
	svc := New(pg, rd)
	r := chi.NewRouter()
	r.Use(svc.RequireCSRF)
	r.Post("/v1/auth/telegram", svc.HandleTelegram)
	r.Post("/v1/auth/anon", svc.HandleAnon)
	r.With(svc.RequireAuth).Post("/v1/auth/link", svc.HandleLink)
	r.With(svc.RequireAuth).Post("/v1/auth/refresh", svc.HandleRefresh)
	r.With(svc.RequireAuth).Post("/v1/auth/logout", svc.HandleLogout)
	// Заодно сам эндпоинт выдачи токена: без него маршрут был бы незасеян, и
	// тесты переноса жили бы в другом роутере, а этот — проверял бы старую
	// ветку link без переноса.
	r.With(svc.RequireAuth).Post("/v1/auth/handoff", svc.HandleHandoff)
	return r, svc, pg
}

func craft(t *testing.T, botToken string, tgID int64) string {
	t.Helper()
	q := url.Values{}
	q.Set("user", `{"id":`+strconv.FormatInt(tgID, 10)+`}`)
	q.Set("auth_date", strconv.FormatInt(time.Now().Unix(), 10))
	pairs := []string{}
	for k, vv := range q {
		pairs = append(pairs, k+"="+strings.Join(vv, ","))
	}
	sort.Strings(pairs)
	mk := hmac.New(sha256.New, []byte("WebAppData"))
	mk.Write([]byte(botToken))
	mac := hmac.New(sha256.New, mk.Sum(nil))
	mac.Write([]byte(strings.Join(pairs, "\n")))
	q.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return q.Encode()
}

// doAuthIP — doAuth с явным RemoteAddr: byIP-лимитеры не должны склеивать
// запросы разных прогонов (A14/F-18.4).
func doAuthIP(r *chi.Mux, tok, method, path, body, csrf, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = ip + ":34567"
	if csrf != "" {
		req.Header.Set("X-CSRF", csrf)
	}
	if tok != "" {
		req.AddCookie(&http.Cookie{Name: CookieName, Value: tok})
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func doAuth(r *chi.Mux, tok, method, path, body, csrf string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF", csrf)
	}
	if tok != "" {
		req.AddCookie(&http.Cookie{Name: CookieName, Value: tok})
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestE2EAuthHandlers(t *testing.T) {
	t.Setenv("TG_BOT_TOKEN", "test-bot")
	t.Setenv("JWT_SECRET", "test-secret-0123456789abcdef0123456789")
	r, svc, pg := authRouterPG(t)
	// Раньше здесь стоял SCAN+DEL по ВСЕМ rl:* — тест вытирал лимитеры других
	// пакетов, идущих параллельно по общему Redis (F-18.3). Теперь у каждого
	// пакета своя Redis-БД (testutil), а идентичность запроса уникальна на
	// прогон, поэтому чистить ничего не нужно.
	// Уникальный IP на прогон: /v1/auth/* ограничен 20/мин по IP, и при
	// `go test -count=2` два прогона делили бы один bucket (F-18.4).
	ip := testutil.UniqueIP(t)
	do := func(tok, method, path, body, csrf string) *httptest.ResponseRecorder {
		return doAuthIP(r, tok, method, path, body, csrf, ip)
	}
	// уникальные tg_id/uuid на прогон (dev-БД общая; nanos полные, не %100000 — коллизии!)
	base := time.Now().UnixNano()

	// Уборка. Тест создаёт трёх юзеров (anon A, TG-юзер, anon B) и раньше
	// ничего не удалял: B съедал DELETE внутри merge, а A и TG-юзер оставались
	// навсегда. Теперь merge ничего не удаляет, и без PurgeUsers каждый прогон
	// оставлял бы три строки — dev-БД зарастает, а лимиты и выборки из неё
	// становятся случайными. PurgeUsers (а не голый DELETE) — на строках висят
	// подписки, платежи и сессии, и FK откатили бы DELETE целиком.
	var made []string
	defer func() { testutil.PurgeUsers(t, context.Background(), pg, made...) }()
	track := func(ids ...string) { made = append(made, ids...) }

	// anon (uuid уникален на прогон — иначе link-состояние перетекает, формат 8-4-4-4-12!)
	uuidA := anonUUID(t, "aaaaaaaa", base)
	rec := do("", "POST", "/v1/auth/anon", `{"uuid":"`+uuidA+`","fingerprint":"anon-fp"}`, "")
	if rec.Code != 200 {
		t.Fatalf("anon: %d %s", rec.Code, rec.Body.String())
	}
	var anon map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &anon)
	cookie := ""
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			cookie = c.Value
		}
	}
	if anon["user_id"] == nil || cookie == "" {
		t.Fatal("no user/cookie")
	}
	track(anon["user_id"].(string))

	// telegram новый → trial 3
	init := craft(t, "test-bot", base+1)
	rec = do("", "POST", "/v1/auth/telegram", `{"initData":"`+url.QueryEscape(init)+`"}`, "")
	// initData уже url-encoded строкой? HandleTelegram ждет сырой initData query-string:
	_ = rec
	initRaw := craft(t, "test-bot", base+2)
	rec = do("", "POST", "/v1/auth/telegram", `{"initData":`+strconv.Quote(initRaw)+`}`, "")
	if rec.Code != 200 {
		t.Fatalf("telegram: %d %s", rec.Code, rec.Body.String())
	}
	var tg map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &tg)
	if tg["is_new"] != true || tg["trial_days"] != float64(3) {
		t.Fatalf("trial: %v", tg)
	}
	track(tg["user_id"].(string))
	tgCookie := ""
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			tgCookie = c.Value
		}
	}

	// link: anon → tg 424244 (свободен) → attach + trial
	init2 := craft(t, "test-bot", base+3)
	rec = do(cookie, "POST", "/v1/auth/link", `{"initData":`+strconv.Quote(init2)+`,"fingerprint":"anon-fp"}`, csrfOf(t, svc, anon["user_id"].(string)))
	if rec.Code != 200 {
		t.Fatalf("link: %d %s", rec.Code, rec.Body.String())
	}
	var linked map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &linked)
	if linked["merged"] != true {
		t.Fatalf("link not merged: %v", linked)
	}

	// повторный link на другой tg → 409
	init3 := craft(t, "test-bot", base+4)
	newCookie := ""
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			newCookie = c.Value
		}
	}
	rec = do(newCookie, "POST", "/v1/auth/link", `{"initData":`+strconv.Quote(init3)+`,"fingerprint":"anon-fp"}`, csrfOf(t, svc, linked["user_id"].(string)))
	if rec.Code != 409 {
		t.Fatalf("relink: want 409 got %d", rec.Code)
	}

	// merge: anon B → занятый tg (base+2): survivor — TG-юзер. B НЕ удаляется,
	// а переводится в status='merged' (миграция 040) — иначе пересланный токен
	// переноса отнял бы покупку навсегда. Поэтому B обязана быть в уборке ниже:
	// раньше её съедал DELETE в самом merge, и тест не оставлял следов.
	recB := do("", "POST", "/v1/auth/anon", `{"uuid":"`+anonUUID(t, "bbbbbbbb", base+1)+`","fingerprint":"anon-b-fp"}`, "")
	if recB.Code != 200 {
		t.Fatalf("anonB: %d %s", recB.Code, recB.Body.String())
	}
	var anonB map[string]any
	_ = json.Unmarshal(recB.Body.Bytes(), &anonB)
	cookieB := ""
	for _, c := range recB.Result().Cookies() {
		if c.Name == CookieName {
			cookieB = c.Value
		}
	}
	uidB, _ := anonB["user_id"].(string)
	track(uidB)
	initTaken := craft(t, "test-bot", base+2)
	rec = do(cookieB, "POST", "/v1/auth/link", `{"initData":`+strconv.Quote(initTaken)+`,"fingerprint":"anon-b-fp"}`, csrfOf(t, svc, anonB["user_id"].(string)))
	if rec.Code != 200 {
		t.Fatalf("merge: %d %s", rec.Code, rec.Body.String())
	}
	var merged map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &merged)
	tgUID, _ := tg["user_id"].(string)
	if merged["user_id"] != tgUID {
		t.Fatalf("merge survivor: %v want %s", merged, tgUID)
	}
	_ = tgCookie
	_ = uidB
}

// hex4 больше НЕ используется: он оставлял 16 бит наносекунд, то есть всего
// 65 536 значений на все прогоны. Коллизия между прогонами означала, что
// AnonLogin возвращал УЖЕ СУЩЕСТВУЮЩЕГО пользователя (ON CONFLICT по
// anon_uuid), а он был связан в прошлом прогоне — тест падал с
// «link: 409 ALREADY_LINKED», и причина была не в коде, а в давней мусоре.
// Идентичность теперь берётся из testutil.UUID (полные 128 бит).
//
//nolint:unused // оставлено как напоминание о границе 16 бит
func hex4(n int64) string {
	s := strconv.FormatInt(n, 16)
	for len(s) < 4 {
		s = "0" + s
	}
	return s[len(s)-4:]
}

// anonUUID — уникальный uuid для анонимной регистрации в тесте.
func anonUUID(t *testing.T, prefix string, n int64) string {
	// 12 hex-символов после последнего дефиса берём из полного наносекундного
	// значения, а 4 символа в середине — из testutil.UUID: так uuid уникален и
	// внутри прогона, и между прогонами.
	uid := testutil.UUID(t)
	parts := strings.Split(uid, "-")
	return prefix + "-" + parts[1] + "-" + parts[2] + "-" + parts[3] + "-" + hex12(n)
}

// hex12 — 12 hex-символов из младших 48 бит n.
func hex12(n int64) string {
	s := strconv.FormatUint(uint64(n)&0xffffffffffff, 16)
	for len(s) < 12 {
		s = "0" + s
	}
	return s[len(s)-12:]
}

func csrfOf(t *testing.T, svc *Service, uid string) string {
	t.Helper()
	v, err := svc.csrfFor(context.Background(), uid)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// testIP — уникальный IP на каждый вызов теста для всего пакета. Регистрация
// анонимных юзеров ограничена 20/час на IP с TTL час (auth.go: rl:reg:<ip>), и
// httptest по умолчанию даёт ВСЕМ тестам один 192.0.2.1: накопленный лимит
// ронял несвязанные тесты (иногда через час после предыдущего прогона), а
// `go test -count=2` — сразу (F-18.4).
func testIP(t *testing.T) string {
	t.Helper()
	return testutil.UniqueIP(t)
}
