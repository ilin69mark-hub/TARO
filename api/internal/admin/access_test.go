// Эндпоинты выдачи/отзыва доступа: контракт HTTP-слоя.
//
// Логика проверяется в internal/access. Здесь важно то, что ломается именно
// на границе: понятная ошибка при перепутанном user_id, идемпотентность отзыва
// и то, что без прав всё закрыто.
//
// Хелперы adminSetup/acall — из admin_e2e_test.go: вход админа ограничен по IP
// и по username в Redis с минутным окном, поэтому каждому тесту нужны свои
// учётка и IP (F-18.4).
package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"taro/api/internal/testutil"
)

// acallOrigin — acall с Origin: запись админки проверяет Origin, и без него
// любой POST упирается в 403, не доходя до обработчика.
func acallOrigin(t *testing.T, r *chi.Mux, tok, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return acallWithHeaders(t, r, tok, method, path, body, map[string]string{"Origin": defaultAdminOrigin})
}

func TestAccessGrantAndList(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	uid := testutil.NewUser(t, ctx, pg)
	r, tok, _, _ := adminSetup(t)

	rec := acallOrigin(t, r, tok, "POST", "/v1/admin/access/grant", `{"user_id":"`+uid+`","days":36500}`)
	if rec.Code != 200 {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		UserID     string `json:"user_id"`
		PlanCode   string `json:"plan_code"`
		ValidUntil string `json:"valid_until"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.UserID != uid || out.PlanCode == "" || out.ValidUntil == "" {
		t.Fatalf("grant response incomplete: %+v", out)
	}

	rec = acall(t, r, tok, "GET", "/v1/admin/access", "")
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var list []struct {
		UserID string `json:"user_id"`
		Active bool   `json:"active"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range list {
		if e.UserID == uid {
			found = true
			if !e.Active {
				t.Fatal("just granted access must be active in list")
			}
		}
	}
	if !found {
		t.Fatalf("granted user missing from list: %+v", list)
	}
}

func TestAccessGrantUnknownUserIsReadable(t *testing.T) {
	r, tok, _, _ := adminSetup(t)
	rec := acallOrigin(t, r, tok, "POST", "/v1/admin/access/grant", `{"user_id":"00000000-0000-0000-0000-000000000000"}`)
	// 404, а не 500 и не пустой 200: оператор чаще всего путает user_id и
	// anon_uuid, и ответ должен это подсказать.
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "user_id") {
		t.Fatalf("error must hint about user_id, got %s", rec.Body.String())
	}
}

func TestAccessGrantRejectsAbsurdDays(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	uid := testutil.NewUser(t, ctx, pg)
	r, tok, _, _ := adminSetup(t)

	// Опечатка вроде 3650000 («тысячу лет») не должна молча выдать доступ.
	rec := acallOrigin(t, r, tok, "POST", "/v1/admin/access/grant", `{"user_id":"`+uid+`","days":3650000}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAccessRevokeIsIdempotent(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	uid := testutil.NewUser(t, ctx, pg)
	r, tok, _, _ := adminSetup(t)

	if rec := acallOrigin(t, r, tok, "POST", "/v1/admin/access/grant", `{"user_id":"`+uid+`"}`); rec.Code != 200 {
		t.Fatalf("grant: %s", rec.Body.String())
	}
	if rec := acallOrigin(t, r, tok, "POST", "/v1/admin/access/revoke", `{"user_id":"`+uid+`"}`); rec.Code != 200 {
		t.Fatalf("revoke: %s", rec.Body.String())
	}
	rec := acallOrigin(t, r, tok, "POST", "/v1/admin/access/revoke", `{"user_id":"`+uid+`"}`)
	// Повторное нажатие «отозвать» не должно пугать оператора ошибкой.
	if rec.Code != 200 {
		t.Fatalf("second revoke must be 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"revoked":0`) {
		t.Fatalf("second revoke must report 0, got %s", rec.Body.String())
	}
}

// Без прав выдача закрыта: иначе кто угодно открыл бы себе безлимит.
func TestAccessRequiresAdmin(t *testing.T) {
	r, _, _, _ := adminSetup(t)
	rec := acallOrigin(t, r, "", "POST", "/v1/admin/access/grant", `{"user_id":"00000000-0000-0000-0000-000000000000"}`)
	if rec.Code != 403 {
		t.Fatalf("want 403 without admin session, got %d", rec.Code)
	}
}
