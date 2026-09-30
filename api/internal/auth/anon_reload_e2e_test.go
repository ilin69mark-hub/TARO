// E2E сценарий, который видел владелец: «все расклады дают 403».
//
// Дефект был не в раскладах, а в связке двух решений:
//  1. Go жёстко ставил fingerprint-cookie с флагом Secure;
//  2. на не-secure origin (http, а не https) браузер такую cookie не хранит.
//
// Клиент после успешного входа НАМЕРЕННО больше не держит fingerprint в
// localStorage (аудит B: иначе XSS крадёт пару и входит как жертва) и шлёт его
// только из cookie. Cookie пропала — тело запроса пустое — Go отвечает
// FP_REQUIRED. Дальше клиент восстанавливается сам (web/lib/auth.ts,
// forgetAnonIdentity), но правильный исход — не доводить до этого вовсе.
//
// Тест повторяет пользовательский путь целиком через настоящий хендлер с
// cookie-jar: первый вход с fingerprint в теле, второй — БЕЗ него, только по
// cookie. На http-origin второй обязан быть 200. Возврат жёсткого Secure
// роняет тест, то есть регрессия не пройдёт тихо.
package auth

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"taro/api/internal/testutil"
)

type anonResult struct {
	code     int
	body     map[string]any
	fpCookie *http.Cookie
}

// postAnon идёт через cookie-jar, поэтому «cookie потерялась» моделируется
// просто: второй вызов несёт jar, а тело без fingerprint — ровно то, что
// делает браузер после перезагрузки страницы.
func postAnon(t *testing.T, srv *httptest.Server, client *http.Client, uuid, fp, proto string) anonResult {
	t.Helper()
	payload := map[string]string{"uuid": uuid}
	if fp != "" {
		payload["fingerprint"] = fp
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/auth/anon", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if proto != "" {
		req.Header.Set("X-Forwarded-Proto", proto)
	}
	// Сессия уже есть -> RequireCSRF ждёт заголовок. Браузер берёт его из
	// cookie taro_csrf (web/lib/api.ts csrf()), поэтому повторяем и мы.
	if v := csrfFromJar(t, client.Jar, req.URL); v != "" {
		req.Header.Set("X-CSRF", v)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	var fpCookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == FpCookie {
			cp := *c
			fpCookie = &cp
		}
	}
	return anonResult{code: res.StatusCode, body: out, fpCookie: fpCookie}
}

// csrfFromJar достаёт значение cookie taro_csrf для URL запроса.
func csrfFromJar(t *testing.T, jar http.CookieJar, u *url.URL) string {
	t.Helper()
	if jar == nil {
		return ""
	}
	for _, c := range jar.Cookies(u) {
		if c.Name == "taro_csrf" {
			return c.Value
		}
	}
	return ""
}

func newAnonServer(t *testing.T) (*httptest.Server, *http.Client, http.CookieJar) {
	t.Helper()
	_, pg, rd := testutil.Live(t)
	svc := New(pg, rd)
	r := chi.NewRouter()
	r.Use(svc.RequireCSRF)
	r.Post("/v1/auth/anon", svc.HandleAnon)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return srv, &http.Client{Jar: jar}, jar
}

func TestAnonLoginSurvivesReloadOnPlainHTTP(t *testing.T) {
	for _, tc := range []struct {
		name   string
		proto  string
		secure bool
	}{
		{"http локальный стенд: cookie обязана пережить перезагрузку", "http", false},
		{"https прод: cookie Secure и переживает перезагрузку", "https", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, client, _ := newAnonServer(t)
			uuid := "00000000-0000-4000-8000-00000000f001"
			fp := "aabbccddeeff00112233445566778899"

			first := postAnon(t, srv, client, uuid, fp, tc.proto)
			if first.code != 200 {
				t.Fatalf("первый вход: %d %v", first.code, first.body)
			}
			if first.fpCookie == nil {
				t.Fatal("fingerprint-cookie не выставлена")
			}
			if first.fpCookie.Secure != tc.secure {
				t.Fatalf("Secure=%v, ожидали %v для схемы %q", first.fpCookie.Secure, tc.secure, tc.proto)
			}

			// Ровно пользовательский сценарий: страница перезагружена, клиент
			// fingerprint не помнит и отправляет только uuid.
			second := postAnon(t, srv, client, uuid, "", tc.proto)
			if second.code != 200 {
				t.Fatalf("вход после перезагрузки: %d %v — клиент остался без сессии", second.code, second.body)
			}
			if second.body["user_id"] != first.body["user_id"] {
				t.Fatalf("сменился user_id: %v -> %v (история анонима потеряна)",
					first.body["user_id"], second.body["user_id"])
			}
		})
	}
}

// Гонка с самим собой: клиент мог получить 403 и сбросить личность. Сервер в
// этом не виноват, но новый вход обязан выдать РАБОЧУЮ сессию, а не снова 403,
// иначе восстановление в web/lib/auth.ts бесконечно.
func TestAnonLoginRecoversAfterFingerprintLoss(t *testing.T) {
	srv, client, _ := newAnonServer(t)
	uuid := "00000000-0000-4000-8000-00000000f002"
	fp := "11223344556677889900aabbccddeeff"

	if r := postAnon(t, srv, client, uuid, fp, "http"); r.code != 200 {
		t.Fatalf("первый вход: %d", r.code)
	}

	// Имитируем потерю cookie: другой jar, тот же uuid, новый fingerprint.
	// Сервер обязан отдать FP_MISMATCH/FP_REQUIRED, а не молча впустить.
	freshJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	freshClient := &http.Client{Jar: freshJar}
	lost := postAnon(t, srv, freshClient, uuid, "ffffffffffffffffffffffffffffffff", "http")
	if lost.code != 403 {
		t.Fatalf("подмена fingerprint: %d %v — ожидался 403", lost.code, lost.body)
	}
	if code, _ := lost.body["error"].(map[string]any)["code"].(string); code != "FP_MISMATCH" && code != "FP_REQUIRED" {
		t.Fatalf("код ошибки %q не про fingerprint", code)
	}

	// Новая анонимная личность (как делает forgetAnonIdentity) обязана работать.
	newUUID := "00000000-0000-4000-8000-00000000f003"
	reborn := postAnon(t, srv, freshClient, newUUID, "aaaabbbbccccddddeeeeffff00001111", "http")
	if reborn.code != 200 {
		t.Fatalf("новая личность не восстановилась: %d %v", reborn.code, reborn.body)
	}
}

// Проводка, а не только флаги: cookie должна реально долететь до браузера и
// обратно. jar не примет Secure-cookie на http — это ровно то поведение, из-за
// которого был 403, поэтому тест обязан падать, если Secure вернётся.
func TestFingerprintCookieRoundTripsThroughJar(t *testing.T) {
	srv, client, jar := newAnonServer(t)
	u, _ := url.Parse(srv.URL)
	uuid := "00000000-0000-4000-8000-00000000f004"

	if r := postAnon(t, srv, client, uuid, "5566778899aabbccddeeff0011223344", "http"); r.code != 200 {
		t.Fatalf("вход: %d", r.code)
	}
	stored := jar.Cookies(u)
	found := false
	for _, c := range stored {
		if c.Name == FpCookie {
			found = true
		}
	}
	if !found {
		t.Fatalf("cookie %s не сохранилась в jar на http-origin: %+v", FpCookie, stored)
	}
}
