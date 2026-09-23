// E2E spreads-кэш против живых PG/Redis (см. D-покрытие, T05).
// Admin-publish тестируется через HTTP e2e (см. D1), чтобы не создавать import-cycle.
package spreads

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"taro/api/internal/testutil"
)

func TestE2ESpreadsCache(t *testing.T) {
	ctx, pg, rd := testutil.Live(t)
	sp := New(pg, rd)

	// miss → 5 базовых (fullmoon/newyear inactive)
	rec := httptest.NewRecorder()
	sp.HandleList(rec, httptest.NewRequest("GET", "/v1/spreads", nil))
	var list []map[string]any
	body := rec.Body.String()
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 5 {
		t.Fatalf("want 5 spreads, got %d: %s", len(list), body[:min(150, len(body))])
	}
	ttl, err := rd.TTL(ctx, CacheKey).Result()
	if err != nil || ttl <= 0 {
		t.Fatalf("no cache TTL: %v %v", ttl, err)
	}

	// деактивируем daily в PG → кэш держит 5
	if _, err := pg.Exec(ctx, `UPDATE spreads SET is_active=false WHERE code='daily'`); err != nil {
		t.Fatal(err)
	}
	rec2 := httptest.NewRecorder()
	sp.HandleList(rec2, httptest.NewRequest("GET", "/v1/spreads", nil))
	var list2 []map[string]any
	_ = json.Unmarshal(rec2.Body.Bytes(), &list2)
	if len(list2) != 5 {
		t.Fatalf("cache must hold 5, got %d", len(list2))
	}

	// возврат как было + инвалидация
	if _, err := pg.Exec(ctx, `UPDATE spreads SET is_active=true WHERE code='daily'`); err != nil {
		t.Fatal(err)
	}
	if err := rd.Del(ctx, CacheKey).Err(); err != nil {
		t.Fatal(err)
	}
	rec3 := httptest.NewRecorder()
	sp.HandleList(rec3, httptest.NewRequest("GET", "/v1/spreads", nil))
	var list3 []map[string]any
	_ = json.Unmarshal(rec3.Body.Bytes(), &list3)
	if len(list3) != 5 {
		t.Fatalf("after invalidate want 5, got %d", len(list3))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
