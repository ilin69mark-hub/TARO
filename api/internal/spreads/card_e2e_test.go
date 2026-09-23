// E2E spreads card + admin login/rotate + me AgeConfirmed (см. D-покрытие).
package spreads

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"taro/api/internal/testutil"
)

func TestE2ECard(t *testing.T) {
	_, pg, rd := testutil.Live(t)
	sp := New(pg, rd)
	r := chi.NewRouter()
	r.Get("/v1/cards/{id}", sp.HandleCard)

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	if rec := get("/v1/cards/0"); rec.Code != 200 {
		t.Fatalf("card 0: %d", rec.Code)
	} else {
		var c map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &c)
		if c["name_ru"] != "Дурак" {
			t.Fatalf("card 0: %v", c)
		}
	}
	for _, bad := range []string{"/v1/cards/78", "/v1/cards/-1", "/v1/cards/xx"} {
		if rec := get(bad); rec.Code != 404 {
			t.Fatalf("%s: want 404 got %d", bad, rec.Code)
		}
	}
	_ = http.StatusOK
}
