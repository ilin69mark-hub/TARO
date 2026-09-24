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

	"taro/api/internal/testutil"
)

func authRouter(t *testing.T) (*chi.Mux, *Service) {
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
	return r, svc
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
	r, svc := authRouter(t)
	// dev-Redis общий: чистим rate-ключи (иначе 20 reg/час бьет по своим же прогонам)
	{
		_, pg, rd := testutil.Live(t)
		_ = pg
		iter := rd.Scan(context.Background(), 0, "rl:*", 100).Iterator()
		for iter.Next(context.Background()) {
			_ = rd.Del(context.Background(), iter.Val()).Err()
		}
	}
	// уникальные tg_id/uuid на прогон (dev-БД общая; nanos полные, не %100000 — коллизии!)
	base := time.Now().UnixNano()

	// anon (uuid уникален на прогон — иначе link-состояние перетекает, формат 8-4-4-4-12!)
	anonUUID := "aaaaaaaa-" + hex4(base) + "-0000-0000-000000000000"
	rec := doAuth(r, "", "POST", "/v1/auth/anon", `{"uuid":"`+anonUUID+`","fingerprint":"anon-fp"}`, "")
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

	// telegram новый → trial 3
	init := craft(t, "test-bot", base+1)
	rec = doAuth(r, "", "POST", "/v1/auth/telegram", `{"initData":"`+url.QueryEscape(init)+`"}`, "")
	// initData уже url-encoded строкой? HandleTelegram ждет сырой initData query-string:
	_ = rec
	initRaw := craft(t, "test-bot", base+2)
	rec = doAuth(r, "", "POST", "/v1/auth/telegram", `{"initData":`+strconv.Quote(initRaw)+`}`, "")
	if rec.Code != 200 {
		t.Fatalf("telegram: %d %s", rec.Code, rec.Body.String())
	}
	var tg map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &tg)
	if tg["is_new"] != true || tg["trial_days"] != float64(3) {
		t.Fatalf("trial: %v", tg)
	}
	tgCookie := ""
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			tgCookie = c.Value
		}
	}

	// link: anon → tg 424244 (свободен) → attach + trial
	init2 := craft(t, "test-bot", base+3)
	rec = doAuth(r, cookie, "POST", "/v1/auth/link", `{"initData":`+strconv.Quote(init2)+`,"fingerprint":"anon-fp"}`, csrfOf(t, svc, anon["user_id"].(string)))
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
	rec = doAuth(r, newCookie, "POST", "/v1/auth/link", `{"initData":`+strconv.Quote(init3)+`,"fingerprint":"anon-fp"}`, csrfOf(t, svc, linked["user_id"].(string)))
	if rec.Code != 409 {
		t.Fatalf("relink: want 409 got %d", rec.Code)
	}

	// merge: anon B → занятый tg (base+2): B удаляется, survivor — TG-юзер
	recB := doAuth(r, "", "POST", "/v1/auth/anon", `{"uuid":"bbbbbbbb-`+hex4(base+1)+`-0000-0000-000000000000","fingerprint":"anon-b-fp"}`, "")
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
	initTaken := craft(t, "test-bot", base+2)
	rec = doAuth(r, cookieB, "POST", "/v1/auth/link", `{"initData":`+strconv.Quote(initTaken)+`,"fingerprint":"anon-b-fp"}`, csrfOf(t, svc, anonB["user_id"].(string)))
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

func hex4(n int64) string {
	s := strconv.FormatInt(n, 16)
	for len(s) < 4 {
		s = "0" + s
	}
	return s[len(s)-4:]
}

func csrfOf(t *testing.T, svc *Service, uid string) string {
	t.Helper()
	v, err := svc.csrfFor(context.Background(), uid)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
