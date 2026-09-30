// E2E rate limiter против живого Redis (см. D-покрытие, V31).
package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"taro/api/internal/testutil"
)

func TestE2ERateLimit(t *testing.T) {
	_, _, rd := testutil.Live(t)
	l := New(rd)
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	h := l.Middleware(ok)

	// Уникальный IP на прогон: лимитер ключует по IP, и жёсткий 9.9.9.9 ломал
	// `go test -count=2` — второй прогон получал 429 там, где ждал 200 (F-18.4).
	ip := testutil.UniqueIP(t)
	fire := func(path string) int {
		req := httptest.NewRequest("POST", path, nil)
		req.RemoteAddr = ip + ":1234"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	// /v1/auth/* — 20/мин: 21-й режут
	for i := 0; i < 20; i++ {
		if code := fire("/v1/auth/anon"); code != 200 {
			t.Fatalf("auth #%d: want 200 got %d", i, code)
		}
	}
	if code := fire("/v1/auth/anon"); code != 429 {
		t.Fatalf("auth #21: want 429 got %d", code)
	}
	// другой префикс — свой бакет
	if code := fire("/v1/spreads"); code != 200 {
		t.Fatalf("spreads: want 200 got %d", code)
	}
}
