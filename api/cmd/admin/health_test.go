package main

// /healthz и /readyz — единственные эндпоинты, которые дёргает оркестратор,
// решая, можно ли переключить трафик на сервис. Именно они оставались без
// тестов: хендлеры были написаны прямо в main().
//
// Проверяем не «200 бывает», а каждую причину отказа. Ошибка здесь стоит
// дорого в обе стороны: проба всегда-зелёная отправляет трафик в сломанный
// сервис, проба всегда-красная не даёт подняться рабочему.
import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

type fakeRedis struct{ err error }

func (f fakeRedis) Ping(context.Context) *redis.StatusCmd {
	return redis.NewStatusResult("PONG", f.err)
}

func TestHealthzAlwaysOK(t *testing.T) {
	// healthz сознательно НЕ проверяет зависимости: он отвечает «процесс жив».
	// Проверка БД здесь означала бы, что падение базы убивает liveness-пробу, и
	// Docker перезапустит контейнер вместо того, чтобы просто убрать трафик.
	rec := httptest.NewRecorder()
	handleHealthz("public")(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz: want 200 got %d", rec.Code)
	}
	if got := rec.Body.String(); got != `{"status":"ok","api":"public"}` {
		t.Fatalf("healthz body: %s", got)
	}
}

func TestAdminReadyz(t *testing.T) {
	t.Setenv("PUBLIC_ORIGIN", "")
	cases := []struct {
		name    string
		origin  string
		db      fakeDB
		cache   fakeRedis
		want    int
		wantMsg string
	}{
		{name: "всё живо", origin: "", db: fakeDB{}, cache: fakeRedis{}, want: 200},
		{name: "origin нормальный", origin: "https://taro.me", db: fakeDB{}, cache: fakeRedis{}, want: 200},
		{name: "БД недоступна", origin: "", db: fakeDB{err: context.DeadlineExceeded},
			cache: fakeRedis{}, want: 503, wantMsg: "database unavailable"},
		{name: "Redis недоступен", origin: "", db: fakeDB{}, cache: fakeRedis{err: context.DeadlineExceeded},
			want: 503, wantMsg: "cache unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PUBLIC_ORIGIN", tc.origin)
			rec := httptest.NewRecorder()
			handleReadyz("PUBLIC_ORIGIN", tc.db, tc.cache, context.Background())(rec, httptest.NewRequest("GET", "/readyz", nil))
			if rec.Code != tc.want {
				t.Fatalf("want %d got %d: %s", tc.want, rec.Code, rec.Body.String())
			}
			if tc.wantMsg != "" && !contains(rec.Body.String(), tc.wantMsg) {
				t.Fatalf("ожидалось «%s», получено %q", tc.wantMsg, rec.Body.String())
			}
		})
	}
}

// Плохой PUBLIC_ORIGIN обязан ронять пробу в 503, а не проходить.
//
// Проверено отдельно от happy path, потому что validateOriginEnv подтверждает
// внешние данные (домен из окружения), и опечатка в самом имени переменной
// выглядела бы как «всё настроено» — сервис принимает трафик, а CORS и
// cookie-защита настроены на другой домен.
func TestAdminReadyzRejectsBadOrigin(t *testing.T) {
	for _, bad := range []string{
		"taro.me",               // без схемы
		"ftp://taro.me",         // не http(s)
		"https://taro.me/path",  // origin без пути
		"https://taro.me?x=1",   // с query
		" https://taro.me",      // с пробелом
		"https://",              // без хоста
		"https://user@taro.me",  // с учёткой
		"https://taro.me:99999", // порт вне диапазона
	} {
		t.Setenv("PUBLIC_ORIGIN", bad)
		rec := httptest.NewRecorder()
		handleReadyz("PUBLIC_ORIGIN", fakeDB{}, fakeRedis{}, context.Background())(rec, httptest.NewRequest("GET", "/readyz", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("PUBLIC_ORIGIN=%q: want 503 got %d", bad, rec.Code)
		}
	}
}

// Проба обязана укладываться в таймаут пробника: если зависимость висит, а мы
// ждём дольше, Docker решит, что контейнер мёртв, и перезапустит его — то есть
// обычный всплеск нагрузки приведёт к рестарту вместо снятия трафика.
func TestAdminReadyzHasBoundedTimeout(t *testing.T) {
	if readyTimeout <= 0 || readyTimeout > 3*time.Second {
		t.Fatalf("readyTimeout=%v: должен быть коротким, иначе пробник успеет решить, что процесс мёртв", readyTimeout)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

// Проба admin-api обязана гаснуть при остановке воркера.
//
// Иначе во время штатного завершения контейнера (SIGTERM → остановка воркера)
// readyz ещё до 2 секунд отвечает «готов», и балансировщик успевает отправить
// запрос в затухающий процесс. Приёмливая канун в этом ровно проигрывает: её
// и написали именно для этого.
func TestAdminReadyzDiesWithWorker(t *testing.T) {
	t.Setenv("ADMIN_ORIGIN", "")
	workerCtx, stopWorker := context.WithCancel(context.Background())

	slowDB := slowPinger{}
	rec := httptest.NewRecorder()
	handleReadyz("ADMIN_ORIGIN", slowDB, fakeRedis{}, workerCtx)(rec, httptest.NewRequest("GET", "/readyz", nil))
	stopWorker()
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("после остановки воркера проба обязана отвечать 503, got %d", rec.Code)
	}
}

type slowPinger struct{}

func (slowPinger) Ping(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
