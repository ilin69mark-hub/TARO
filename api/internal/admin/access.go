// Выдача и отзыв безлимита через админ-API.
//
// Логика живёт в internal/access — она общая с adminctl. Здесь только HTTP:
// разбор, валидация, права (RequireAdmin снаружи) и понятные ошибки.
//
// Почему в админке вообще: безлимит = активная подписка, а не роль. Владелец
// и тестировщики регулярно просили «снять лимит», и пока это делалось SQL
// руками, каждый раз упиралось в source_type ('legacy'|'payment').
package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"taro/api/internal/access"
	"taro/api/internal/apierr"
)

// HandleAccessGrant — POST /v1/admin/access/grant {user_id, days?}.
func (s *Service) HandleAccessGrant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID string `json:"user_id"`
		Days   int    `json:"days"`
	}
	if !apierr.Decode(w, r, &req) || req.UserID == "" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нужен user_id")
		return
	}
	// Верхняя граница: «навсегда» — это 100 лет (DefaultDays). Больше не нужно,
	// а ограничение защищает от опечатки (36500 → 3650000 → 10 000 лет).
	if req.Days < 0 || req.Days > access.DefaultDays {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation,
			"days должен быть от 1 до "+strconv.Itoa(access.DefaultDays))
		return
	}
	until, err := access.Grant(r.Context(), s.pg, req.UserID, req.Days)
	switch {
	case errors.Is(err, access.ErrNoUser):
		apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Пользователь не найден — проверь user_id, не anon_uuid")
		return
	case errors.Is(err, access.ErrNoPlan):
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "В plans нет кода "+access.DefaultPlanCode)
		return
	case err != nil:
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось выдать доступ")
		return
	}
	writeJSON(w, map[string]any{"user_id": req.UserID, "valid_until": until, "plan_code": access.DefaultPlanCode})
}

// HandleAccessRevoke — POST /v1/admin/access/revoke {user_id}.
func (s *Service) HandleAccessRevoke(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID string `json:"user_id"`
	}
	if !apierr.Decode(w, r, &req) || req.UserID == "" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нужен user_id")
		return
	}
	n, err := access.Revoke(r.Context(), s.pg, req.UserID)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось отозвать доступ")
		return
	}
	// revoked=0 — не ошибка: идемпотентность важнее, чтобы повторное нажатие
	// «отозвать» не пугало оператора красным сообщением.
	writeJSON(w, map[string]any{"user_id": req.UserID, "revoked": n})
}

// HandleAccessList — GET /v1/admin/access?limit=N.
func (s *Service) HandleAccessList(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 200 {
		limit = n
	}
	entries, err := access.List(r.Context(), s.pg)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось загрузить список")
		return
	}
	if len(entries) > limit {
		entries = entries[:limit]
	}
	// Помечаем, кто уже имеет действующий доступ прямо сейчас: оператор смотрит
	// на список и должен видеть разницу между «до 2126 года» и «отозван».
	now := time.Now()
	for i := range entries {
		entries[i].Active = entries[i].Status == "active" && entries[i].ValidUntil.After(now)
	}
	writeJSON(w, entries)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// RegisterAccessRoutes — единственное место, где объявляются роуты доступа.
// Используется и в cmd/admin, и в тестах: когда роутер собирается вручную в
// тесте, а в проде — отдельно, registration расходится молча, и снятый
// RequireAdmin или забытый роут находят себя уже на стенде, а не в тестах.
func (s *Service) RegisterAccessRoutes(r chi.Router) {
	r.With(s.RequireAdmin).Post("/v1/admin/access/grant", s.HandleAccessGrant)
	r.With(s.RequireAdmin).Post("/v1/admin/access/revoke", s.HandleAccessRevoke)
	r.With(s.RequireAdmin).Get("/v1/admin/access", s.HandleAccessList)
}
