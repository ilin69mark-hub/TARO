// Очистка Redis только после коммита удаления (A08/F-08).
//
// Раньше sess/csrf/ent чистились ДО транзакции. Любой откат (например, 40P01
// из-за конкурентного вебхука) оставлял пользователя с живой учётной записью,
// но без сессии и без квот — принудительный повторный логин поверх рабочего
// аккаунта. Тест роняет транзакцию детерминированно и проверяет, что ключи
// на месте.
package me

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"taro/api/internal/auth"
	"taro/api/internal/testutil"
)

func TestDeleteFailureKeepsSession(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	au := auth.New(pg, rd)
	mv := New(pg, rd)

	uid := testutil.NewUser(t, ctx, pg)
	tok, err := auth.IssueJWT(uid, auth.UserTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err := rd.Set(ctx, "sess:"+uid, "x", auth.UserTTL).Err(); err != nil {
		t.Fatal(err)
	}
	if err := rd.Set(ctx, "csrf:"+uid, "c", auth.UserTTL).Err(); err != nil {
		t.Fatal(err)
	}
	if err := rd.Set(ctx, "ent:"+uid+":day", "1", auth.UserTTL).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = rd.Del(context.Background(), "sess:"+uid, "csrf:"+uid, "ent:"+uid+":day").Err()
	})

	// Транзакция удаления обязана упасть.
	//
	// ВАЖНО (изоляция тестов): триггер на `users` — ГЛОБАЛЬНЫЙ объект общей БД,
	// а `go test ./...` гоняет пакеты параллельно. Без WHEN условия триггер
	// ронял ЛЮБОЕ удаление пользователя, включая слияние аккаунтов в пакете
	// `auth` (Link удаляет проигравшего юзера) — из-за этого прогон падал в
	// чужом пакете. Условие по OLD.id оставляет отказ ровно для своей строки.
	const fn = "taro_test_fail_user_delete"
	if _, err := pg.Exec(ctx, `
		CREATE OR REPLACE FUNCTION `+fn+`() RETURNS trigger AS $f$
		BEGIN
			RAISE EXCEPTION 'forced failure for A08 test';
		END $f$ LANGUAGE plpgsql`); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `DROP TRIGGER IF EXISTS taro_test_fail_del ON users`); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `
		CREATE TRIGGER taro_test_fail_del BEFORE DELETE ON users
		FOR EACH ROW WHEN (OLD.id = '`+uid+`'::uuid) EXECUTE FUNCTION `+fn+`()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pg.Exec(bg, `DROP TRIGGER IF EXISTS taro_test_fail_del ON users`)
		_, _ = pg.Exec(bg, `DROP FUNCTION IF EXISTS `+fn+`()`)
	})

	req := httptest.NewRequest("DELETE", "/v1/me", strings.NewReader(`{"confirm":"DELETE"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF", "test")
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	mux.Handle("/v1/me", au.RequireAuth(http.HandlerFunc(mv.HandleDelete)))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected forced 500, got %d: %s", rec.Code, rec.Body.String())
	}

	// Учётная запись жива (транзакция откатилась) — значит, живы и ключи сессии.
	var stillThere bool
	if err := pg.QueryRow(ctx, `SELECT true FROM users WHERE id=$1`, uid).Scan(&stillThere); err != nil {
		t.Fatalf("user must survive a rolled back delete: %v", err)
	}
	for _, key := range []string{"sess:" + uid, "csrf:" + uid, "ent:" + uid + ":day"} {
		n, err := rd.Exists(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("redis key %s was purged although the delete transaction rolled back", key)
		}
	}
}
