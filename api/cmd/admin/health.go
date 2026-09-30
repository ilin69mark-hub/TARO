package main

// Проба готовности: /healthz и /readyz.
//
// Раньше оба хендлера были написаны прямо в main() и не имели ни одного теста —
// при том что именно /readyz дёргает оркестратор, решая, можно ли переключать на
// сервис трафик. Опечатка в имени переменной или забытая зависимость здесь были
// видны только на проде, в момент деплоя.
//
// Вынесено в отдельный файл с интерфейсами вместо конкретных клиентов: так
// «БД недоступна» и «Redis недоступен» проверяются настоящим отказом, а не
// выключенной заглушкой.
import (
	"context"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

type dbPinger interface {
	Ping(ctx context.Context) error
}

type cachePinger interface {
	Ping(ctx context.Context) *redis.StatusCmd
}

// readyTimeout — сколько ждём зависимости, прежде чем признать сервис
// неготовым. Короче таймаутов health-check'ов в docker-compose и nginx, иначе
// probe не успевает и успешный сервис будет объявлен мёртвым.
const readyTimeout = 2 * time.Second

func handleHealthz(api string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","api":"` + api + `"}`))
	}
}

// handleReadyz — готовность = конфигурация валидна + БД отвечает + Redis
// отвечает. Порядок важен: сначала дешёвая проверка окружения, потом сеть, чтобы
// при плохом ADMIN_ORIGIN не тратить 2 секуты на пинги.
//
// workerCtx — контекст воркера AI: при его остановке (SIGTERM) проба гасится
// немедленно, иначе во время штатного завершения контейнера readyz ещё
// 2 секунды отвечает «готов», и балансировщик успевает послать запрос в
// затухающий процесс.
func handleReadyz(originEnv string, pg dbPinger, rd cachePinger, workerCtx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if err := validateOriginEnv(originEnv); err != nil {
			http.Error(w, "invalid "+originEnv, http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), readyTimeout)
		stopOnWorkerStop := context.AfterFunc(workerCtx, cancel)
		defer stopOnWorkerStop()
		defer cancel()
		if err := pg.Ping(ctx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := rd.Ping(ctx).Err(); err != nil {
			http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ready","api":"` + apiName(originEnv) + `"}`))
	}
}

// apiName — имя api в ответе. Оно же служит проверкой, что в роутере public, а
// не admin: у admin-api в /readyz должно быть "admin".
func apiName(originEnv string) string {
	if originEnv == "ADMIN_ORIGIN" {
		return "admin"
	}
	return "public"
}
