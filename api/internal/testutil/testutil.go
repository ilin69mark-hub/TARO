// Общие хелперы живых тестов: PG+Redis из env (см. D-покрытие 70%).
// Локально: DATABASE_URL=...:5443 REDIS_ADDR=...:6391 (см. T15: хост-порты через env).
// В CI сервисы на 5432/6379 + migrate.sh до тестов (см. ci.yml).
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"taro/api/internal/store"
)

// Live возвращает пул PG и Redis или Skip без env.
func Live(t *testing.T) (context.Context, *pgxpool.Pool, *redis.Client) {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("no DATABASE_URL")
	}
	ctx := context.Background()
	pg, err := store.ConnectPG(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pg.Close() })
	// Изоляция тестов Redis сделана на идентичностях, а не на затирании: у Redis
	// всего 16 БД, а пакетов больше, так что «своя БД на пакет» давал
	// СТОЛКНОВЕНИЯ (ratelimit/readings/spreads → одна БД) и снова смешивал
	// состояние (F-18.3). Поэтому: уникальные IP (UniqueIP), уникальные
	// user_id (UUID), уникальные tg/anon-uuid и prompt — см. A14/F-18.
	rd := store.ConnectRedis()
	t.Cleanup(func() { rd.Close() })
	var one int
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM spreads`).Scan(&one); err != nil {
		t.Fatalf("db not migrated (run deploy/migrate.sh up): %v", err)
	}
	return ctx, pg, rd
}

// NewUser создает anon-юзера, возвращает id. Чистит за собой.
func NewUser(t *testing.T, ctx context.Context, pg *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pg.QueryRow(ctx,
		`INSERT INTO users (anon_uuid) VALUES (gen_random_uuid()) RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id) })
	return id
}

var (
	uniqueCounter atomic.Int64
	uniqueBase    int64
	uniqueOnce    sync.Once
)

// uniqueIPFirstOctets — адреса выдаются только из этих префиксов: 10/8 (private),
// 172.16/12 (private) и два блока документации RFC 5737. Первый окет берётся из
// этого списка, поэтому адрес не может случайно оказаться в link-local,
// multicast или зарезервированном диапазоне.
var uniqueIPFirstOctets = [5]byte{10, 172, 192, 198, 203}

// uniqueIPPerProcess — сколько адресов выдаёт один процесс до повтора.
// 4096 — с запасом больше, чем делает любой пакет (admin делает ~200).
const uniqueIPPerProcess = 4096

// UniqueIP — уникальный IP для RemoteAddr. Нужен там, где лимитер ключует по
// IP: иначе второй прогон (`go test -count=2`) упирается в лимит, выбитый
// первым (A14/F-18.4).
//
// A17/F-43, регресс: прежняя формула `base*1000 + counter` с первым октетом
// `&0x3f` ОБРЕЗАЛА адрес до 22 бит, причём n и n+2^22 давали один и тот же IP.
// При ~10 прогонах админского пакета за окно лимита входа (15 минут) коллизии
// баз (~1/4000) случались регулярно, и TestE2EAdminLoginRateLimit падал с
// «attempt 1: want 401 got 429»: исход теста зависел от того, гоняли ли его за
// последние 15 минут.
//
// Схема теперь без обрезания: 20 бит базы на процесс, из них 3 бита выбирают
// первый окет, 17 бит идут в старшие биты адреса, младшие 12 бит — счётчик.
// Внутри процесса адреса не повторяются (4096 штук), между процессами коллизия
// возможна только при совпадении 20-битной базы (1/2^20).
//
// Чего генератор НЕ даёт: вероятностной защиты от коллизии между процессами.
// Настоящая защита — владение собственными ключами: тест, проверяющий лимит по
// IP, чистит выведенные из СВОИХ идентичностей ключи (см.
// TestE2EAdminLoginRateLimit).
func UniqueIP(t *testing.T) string {
	t.Helper()
	uniqueOnce.Do(func() {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err == nil {
			uniqueBase = int64(binary.LittleEndian.Uint64(b) & 0xfffff)
			return
		}
		uniqueBase = time.Now().UnixNano() & 0xfffff
	})
	n := (uniqueBase>>3)<<12 | uniqueCounter.Add(1)&(uniqueIPPerProcess-1)
	// Индекс берём по модулю длины списка, а НЕ маской: маска `&7` при списке
	// из 5 октетов давала индекс 5..7 и паникой (вскрыто тестом на -count=2).
	first := uniqueIPFirstOctets[uniqueBase%int64(len(uniqueIPFirstOctets))]
	return fmt.Sprintf("%d.%d.%d.%d", first, (n>>16)&0xff, (n>>8)&0xff, n&0xff)
}

// UUID — уникальный идентификатор в форме UUID, без записи в БД. Нужен там, где
// лимитер ключует по user_id из JWT: фиксированный subject переживал
// `go test -count=2` и второй прогон сразу получал 429 (A14/F-18.4).
func UUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("uuid: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
