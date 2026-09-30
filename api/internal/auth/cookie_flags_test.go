// Флаги cookie решают, сохранит ли браузер fingerprint. Ошибка здесь стоит
// пользователю анонимной личности: cookie не сохранена -> клиент не может её
// восстановить -> 403 FP_REQUIRED -> вход anon невозможен (см. web/lib/auth.ts,
// forgetAnonIdentity). Поэтому оба состояния схемы зафиксированы тестом.
package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func requestWithProto(proto string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "http://localhost/api/auth/anon", nil)
	if proto != "" {
		r.Header.Set("X-Forwarded-Proto", proto)
	}
	return r
}

func TestCookieFlagsFollowClientScheme(t *testing.T) {
	cases := []struct {
		name       string
		proto      string
		wantSecure bool
		wantSame   http.SameSite
	}{
		{"https прод: Secure обязателен для TG iframe", "https", true, http.SameSiteNoneMode},
		{"HTTPS в верхнем регистре", "HTTPS", true, http.SameSiteNoneMode},
		{"http локальный стенд: Secure выключаем, иначе cookie теряется", "http", false, http.SameSiteLaxMode},
		{"заголовка нет — считаем http", "", false, http.SameSiteLaxMode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			secure, same := CookieFlags(requestWithProto(tc.proto))
			if secure != tc.wantSecure {
				t.Fatalf("Secure=%v, want %v", secure, tc.wantSecure)
			}
			if same != tc.wantSame {
				t.Fatalf("SameSite=%v, want %v", same, tc.wantSame)
			}
		})
	}
}

// SameSite=None без Secure браузеры отвергают целиком — это не «ослабление»,
// это cookie, которая не работает. Пара флагов обязана быть согласована.
func TestSameSiteNoneNeverWithoutSecure(t *testing.T) {
	for _, proto := range []string{"https", "http", ""} {
		secure, same := CookieFlags(requestWithProto(proto))
		if same == http.SameSiteNoneMode && !secure {
			t.Fatalf("proto=%q: SameSite=None без Secure — браузер отвергнет cookie", proto)
		}
	}
}

func TestWriteFpCookieAndSessionUseSchemeAwareFlags(t *testing.T) {
	for _, tc := range []struct {
		proto string
		https bool
	}{{"https", true}, {"http", false}} {
		w := httptest.NewRecorder()
		r := requestWithProto(tc.proto)
		writeFpCookie(w, r, "fp-123")
		writeCookie(w, r, "jwt-456", time.Hour)

		rec := w.Result()
		defer rec.Body.Close()
		cookies := rec.Cookies()
		if len(cookies) != 2 {
			t.Fatalf("proto=%q: %d cookie(s), want 2", tc.proto, len(cookies))
		}
		for _, c := range cookies {
			if c.Secure != tc.https {
				t.Fatalf("proto=%q: cookie %s Secure=%v, want %v", tc.proto, c.Name, c.Secure, tc.https)
			}
			if !c.HttpOnly {
				t.Fatalf("proto=%q: cookie %s не HttpOnly", tc.proto, c.Name)
			}
		}
	}
}

// Флаги удаления обязаны совпадать с флагами постановки, иначе браузер не
// сопоставит cookie при logout и оставит её жить.
func TestExpireAuthCookiesMatchSetFlags(t *testing.T) {
	for _, proto := range []string{"https", "http"} {
		setRec := httptest.NewRecorder()
		r := requestWithProto(proto)
		writeFpCookie(setRec, r, "fp-123")
		set := setRec.Result()
		setFlags := map[string]bool{}
		for _, c := range set.Cookies() {
			setFlags[c.Name] = c.Secure
		}

		expRec := httptest.NewRecorder()
		ExpireAuthCookies(expRec, r)
		exp := expRec.Result()
		defer exp.Body.Close()
		for _, c := range exp.Cookies() {
			if c.MaxAge >= 0 {
				t.Fatalf("proto=%q: cookie %s не удаляется (Max-Age=%d)", proto, c.Name, c.MaxAge)
			}
			if want, ok := setFlags[c.Name]; ok && c.Secure != want {
				t.Fatalf("proto=%q: удаление %s Secure=%v, а постановка была %v", proto, c.Name, c.Secure, want)
			}
		}
	}
}

// Регрессия: без этого теста легко вернуть жёсткий Secure: true, и на
// локальном стенде (и на любом http-доступе) вход anon снова станет невозможен.
func TestFingerprintCookieSurvivesPlainHTTPOrigin(t *testing.T) {
	w := httptest.NewRecorder()
	r := requestWithProto("http")
	writeFpCookie(w, r, "fp-plain")
	header := w.Result().Header.Get("Set-Cookie")

	parsed, err := url.Parse("http://localhost/")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(header, "Secure") {
		t.Fatalf("fp-cookie помечен Secure на http-origin и не сохранится: %s", header)
	}
	if strings.Contains(header, "SameSite=None") {
		t.Fatalf("SameSite=None без Secure браузер отвергнет: %s", header)
	}
	if parsed.Host != "localhost" {
		t.Fatalf("unexpected host %s", parsed.Host)
	}
	if !strings.Contains(header, FpCookie+"=fp-plain") {
		t.Fatalf("fp-cookie не выставлен: %s", header)
	}
}
