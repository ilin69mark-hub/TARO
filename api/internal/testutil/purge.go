package testutil

// Удаление тестовых пользователей вместе со всем, что на них ссылается.
//
// Зачем это в testutil, а не в каждом пакете: `DELETE FROM users WHERE id=ANY($1)`
// с заглушенной ошибкой — не чистка, а иллюзия. У плательщика есть подписки,
// платежи, чтения, рефералы, и на них висят FK, поэтому такой DELETE откатывается
// целиком и не удаляет НИЧЕГО. Тест при этом «зелёный», мусор копится, и через
// несколько прогонов тест падает уже не на своей логике, а на duplicate key по
// чужой строке.
//
// Имена таблиц и колонок берутся из pg_constraint, а не из соглашения: у
// referrals это referrer_id и referee_id, у admin_audit — admin_id, и
// предположение «везде user_id» оборачивается ошибкой "column user_id does not
// exist" на первой же чистке. Новые таблицы тоже подхватываются сами.
import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func childCols(ctx context.Context, pg *pgxpool.Pool) ([][2]string, error) {
	rows, err := pg.Query(ctx, `
		SELECT DISTINCT c.conrelid::regclass::text, a.attname
		FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		JOIN unnest(c.conkey) AS k(attnum) ON true
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = k.attnum
		WHERE c.contype = 'f' AND c.confrelid = 'users'::regclass AND n.nspname = 'public'
		ORDER BY 1, 2`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var tbl, col string
		if err := rows.Scan(&tbl, &col); err != nil {
			return nil, err
		}
		out = append(out, [2]string{tbl, col})
	}
	return out, rows.Err()
}

// PurgeUsers сносит пользователей и все записи, ссылающиеся на них.
func PurgeUsers(t *testing.T, ctx context.Context, pg *pgxpool.Pool, ids ...string) {
	t.Helper()
	if len(ids) == 0 {
		return
	}
	cols, err := childCols(ctx, pg)
	if err != nil {
		t.Fatalf("список зависимостей: %v", err)
	}
	// Два прохода: после «детей» на users могут остаться ссылки от их детей,
	// и вложенность будущих таблиц этим не гарантирована.
	for pass := range 2 {
		for _, c := range cols {
			// Имена приходят из information_schema, а не из ввода теста,
			// поэтому format без кавычек здесь безопасен.
			q := fmt.Sprintf(`DELETE FROM %s WHERE %s = ANY($1)`, c[0], c[1])
			if _, err := pg.Exec(ctx, q, ids); err != nil {
				t.Fatalf("чистка %s.%s: %v", c[0], c[1], err)
			}
		}
		if _, err := pg.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, ids); err != nil {
			if pass == 1 {
				t.Fatalf("чистка users: %v", err)
			}
		}
	}
}
