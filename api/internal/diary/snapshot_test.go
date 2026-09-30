// Запись дневника должна нести снимок расклада: карты + вопрос + толкование.
// Владелец: «как мне из дневника вернуться обратно к раскладу, который я
// решил записать? И как-то расклад наверное должен сохраняться в записи, чтобы
// я мог потом и прочитать и посмотреть снова».
//
// Раньше запись хранила только reading_id, а список его не отдавал: из дневника
// было не попасть на расклад вообще, и мысли читались в отрыве от того, что их
// вызвало. Теперь это LEFT JOIN в HandleList, а не N+1 запросов на клиенте.
package diary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"taro/api/internal/auth"
	"taro/api/internal/testutil"
)

// seedReadingWithDiary создаёт чтение с картами и запись, привязанную к нему.
func seedReadingWithDiary(t *testing.T, ctx context.Context, pg *pgxpool.Pool, uid, cardsJSON string) string {
	t.Helper()
	var rid string
	if err := pg.QueryRow(ctx, `INSERT INTO readings (user_id, spread_code, question, cards, interpretation, seed, status, quota_state)
		VALUES ($1,'three','Стоит ли двигаться?', $2::jsonb, 'Толкование расклада.', 1, 'done','allowed') RETURNING id`, uid, cardsJSON).Scan(&rid); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `INSERT INTO diary_entries (user_id, reading_id, body, mood) VALUES ($1,$2,'Мои мысли','up')`, uid, rid); err != nil {
		t.Fatal(err)
	}
	return rid
}

func TestDiaryListCarriesReadingSnapshot(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg)
	au := auth.New(pg, rd)
	r := chi.NewRouter()
	r.With(au.RequireAuth).Get("/v1/diary", svc.HandleList)
	uid := testutil.NewUser(t, ctx, pg)
	tok, err := auth.IssueJWT(uid, auth.UserTTL)
	if err != nil {
		t.Fatal(err)
	}
	// RequireAuth проверяет и JWT, и ЖИВУЮ сессию в Redis — без неё 401.
	if err := rd.Set(ctx, "sess:"+uid, "x", auth.UserTTL).Err(); err != nil {
		t.Fatal(err)
	}
	cards := `[{"card_id":11,"position":0,"name_ru":"Жрица","image_key":"cards/major-02-priestess.webp","reversed":false},
	           {"card_id":22,"position":1,"name_ru":"Двойка Кубков","image_key":"cards/minor-cups-02.webp","reversed":true}]`
	seedReadingWithDiary(t, ctx, pg, uid, cards)

	req := httptest.NewRequest("GET", "/v1/diary?limit=20", nil)
	req.Header.Set("Cookie", auth.CookieName+"="+tok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var out []Entry
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("записей %d, ждали 1", len(out))
	}
	e := out[0]
	if e.ReadingID == nil {
		t.Fatal("reading_id не вернулся — из дневника не попасть на расклад")
	}
	if e.Question == nil || !strings.Contains(*e.Question, "двигаться") {
		t.Fatalf("вопрос расклада не вернулся: %v", e.Question)
	}
	if e.Interpretation == nil || *e.Interpretation == "" {
		t.Fatal("толкование не вернулось — мысли не читаются вместе с раскладом")
	}
	if len(e.Cards) != 2 {
		t.Fatalf("карт %d, ждали 2", len(e.Cards))
	}
	// image_key обязателен: клиент сам склеивает /<image_key> (контракт A05/F-04).
	if e.Cards[0].ImageKey != "cards/major-02-priestess.webp" {
		t.Fatalf("image_key карты: %q", e.Cards[0].ImageKey)
	}
	if !e.Cards[1].Reversed {
		t.Fatal("перевёрнутость карты потерялась")
	}
}

// Запись без расклада — обычный случай (пользователь просто пишет заметку).
// Снимок должен быть пустым, а не ломать вывод.
func TestDiaryListWithoutReading(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg)
	au := auth.New(pg, rd)
	r := chi.NewRouter()
	r.With(au.RequireAuth).Get("/v1/diary", svc.HandleList)
	uid := testutil.NewUser(t, ctx, pg)
	tok, err := auth.IssueJWT(uid, auth.UserTTL)
	if err != nil {
		t.Fatal(err)
	}
	// RequireAuth проверяет и JWT, и ЖИВУЮ сессию в Redis — без неё 401.
	if err := rd.Set(ctx, "sess:"+uid, "x", auth.UserTTL).Err(); err != nil {
		t.Fatal(err)
	}
	_, _ = pg.Exec(ctx, `INSERT INTO diary_entries (user_id, body) VALUES ($1,'Просто заметка')`, uid)

	req := httptest.NewRequest("GET", "/v1/diary", nil)
	req.Header.Set("Cookie", auth.CookieName+"="+tok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("list: %d", rec.Code)
	}
	var out []Entry
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("записей %d", len(out))
	}
	if out[0].ReadingID != nil {
		t.Fatal("reading_id должен быть nil")
	}
	// cards обязан быть [] а не null — клиент делает cards?.length без guard,
	// и null молча ломает проверку «есть ли снимок».
	if out[0].Cards == nil {
		t.Fatal("cards=null: клиент ждёт массив")
	}
	if len(out[0].Cards) != 0 {
		t.Fatalf("cards=%d, ждали пусто", len(out[0].Cards))
	}
}

// LEFT JOIN не должен утекать чужие расклады: запись ссылается только на
// чтение СВОЕГО юзера (FK это гарантирует), но проверяем, что снимок пустой,
// если чтение удалено (ON DELETE SET NULL).
func TestDiarySnapshotEmptyAfterReadingDeleted(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg)
	au := auth.New(pg, rd)
	r := chi.NewRouter()
	r.With(au.RequireAuth).Get("/v1/diary", svc.HandleList)
	uid := testutil.NewUser(t, ctx, pg)
	tok, err := auth.IssueJWT(uid, auth.UserTTL)
	if err != nil {
		t.Fatal(err)
	}
	// RequireAuth проверяет и JWT, и ЖИВУЮ сессию в Redis — без неё 401.
	if err := rd.Set(ctx, "sess:"+uid, "x", auth.UserTTL).Err(); err != nil {
		t.Fatal(err)
	}
	rid := seedReadingWithDiary(t, ctx, pg, uid, `[{"card_id":1,"position":0,"name_ru":"Дурак","image_key":"cards/major-00-fool.webp","reversed":false}]`)
	if _, err := pg.Exec(ctx, `DELETE FROM readings WHERE id=$1`, rid); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/v1/diary", nil)
	req.Header.Set("Cookie", auth.CookieName+"="+tok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("list: %d", rec.Code)
	}
	var out []Entry
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("записей %d", len(out))
	}
	if out[0].ReadingID != nil {
		t.Fatal("после удаления чтения reading_id должен стать null")
	}
	if len(out[0].Cards) != 0 {
		t.Fatalf("снимок не должен пережить удаление чтения: %d карт", len(out[0].Cards))
	}
}

// Снимок в дневнике НЕ ДОЛЖЕН стать обходом подписки: без подписки толкование
// вчерашнего чтения не отдаётся — ровно как в GET /v1/readings. Без этой
// проверки можно было бы читать все старые толкования через дневник.
func TestDiarySnapshotRespectsSubscription(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg)
	au := auth.New(pg, rd)
	r := chi.NewRouter()
	r.With(au.RequireAuth).Get("/v1/diary", svc.HandleList)
	uid := testutil.NewUser(t, ctx, pg)
	tok, err := auth.IssueJWT(uid, auth.UserTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err := rd.Set(ctx, "sess:"+uid, "x", auth.UserTTL).Err(); err != nil {
		t.Fatal(err)
	}
	// created_at — вчера: без подписки должно быть locked.
	var rid string
	if err := pg.QueryRow(ctx, `INSERT INTO readings (user_id, spread_code, question, cards, interpretation, seed, status, quota_state, created_at)
		VALUES ($1,'three','Вчерашний вопрос','[{"card_id":1,"position":0,"name_ru":"Дурак","image_key":"cards/major-00-fool.webp","reversed":false}]'::jsonb,
		'Секретное толкование', 1, 'done','allowed', now() - interval '2 days') RETURNING id`, uid).Scan(&rid); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `INSERT INTO diary_entries (user_id, reading_id, body) VALUES ($1,$2,'Заметка')`, uid, rid); err != nil {
		t.Fatal(err)
	}

	get := func() Entry {
		req := httptest.NewRequest("GET", "/v1/diary", nil)
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
		}
		var out []Entry
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 {
			t.Fatalf("записей %d", len(out))
		}
		return out[0]
	}

	e := get()
	if e.ReadingLocked == nil || !*e.ReadingLocked {
		t.Fatalf("вчерашнее чтение без подписки должно быть locked, а не отдано: %+v", e.ReadingLocked)
	}
	if e.Interpretation != nil {
		t.Fatalf("толкование вчерашнего чтения утекло через дневник: %q", *e.Interpretation)
	}
	// Карты остаются: они не платные, только толкование.
	if len(e.Cards) != 1 {
		t.Fatalf("карты должны остаться доступны: %d", len(e.Cards))
	}

	// С подпиской толкование возвращается.
	var planID string
	if err := pg.QueryRow(ctx, `SELECT id FROM plans LIMIT 1`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, valid_until)
		VALUES ($1,$2,'unlimited',0, now() + interval '30 days')`, uid, planID); err != nil {
		t.Fatal(err)
	}
	e = get()
	if e.Interpretation == nil || *e.Interpretation == "" {
		t.Fatal("с подпиской толкование должно возвращаться")
	}
	if e.ReadingLocked == nil || *e.ReadingLocked {
		t.Fatal("с подпиской locked должен быть false")
	}
}
