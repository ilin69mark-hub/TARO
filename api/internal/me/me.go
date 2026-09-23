// Package me — удаление данных и age-gate (см. docs/project-book/08-risks/02, T15).
// DELETE /v1/me: Redis сразу (sess + ent:* через SCAN точечно по user_id),
// PG — DELETE users (CASCADE чистит readings/subscriptions/payments/entitlements/referrals;
// ai_logs сохраняет строки с reading_id=NULL — анонимизация, см. схему).
// PG-бэкапы тлеют ≤30д — retention настраивается в T30 (backup rotation).
// POST /v1/me/age {confirmed:true} ставит age_confirmed_at; без него T29 блокирует оплату.
package me

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"taro/api/internal/apierr"
	"taro/api/internal/auth"
)

// Service — удаление и возраст.
type Service struct {
	pg *pgxpool.Pool
	rd *redis.Client
}

// New возвращает сервис.
func New(pg *pgxpool.Pool, rd *redis.Client) *Service {
	return &Service{pg: pg, rd: rd}
}

// HandleDelete — DELETE /v1/me: стирает все + гасит cookie. Ответ 200.
func (s *Service) HandleDelete(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	ctx := r.Context()
	// Redis: sess + все ent/love ключи юзера (SCAN, не KEYS — см. 05-cache-redis.md)
	_ = s.rd.Del(ctx, "sess:"+uid).Err()
	var cursor uint64
	for {
		keys, next, err := s.rd.Scan(ctx, cursor, "ent:"+uid+":*", 100).Result()
		if err != nil {
			break
		}
		if len(keys) > 0 {
			_ = s.rd.Del(ctx, keys...).Err()
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	// admin_audit без CASCADE — чистим явно (админ тоже может удалиться)
	_, _ = s.pg.Exec(ctx, `DELETE FROM admin_audit WHERE admin_id=$1`, uid)
	_, err := s.pg.Exec(ctx, `DELETE FROM users WHERE id=$1`, uid)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось удалить данные")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: auth.CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// HandleAge — POST /v1/me/age {confirmed:true}: фиксирует 18+.
func (s *Service) HandleAge(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	var req struct {
		Confirmed bool `json:"confirmed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.Confirmed {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нужно подтверждение 18+")
		return
	}
	if _, err := s.pg.Exec(r.Context(),
		`UPDATE users SET age_confirmed_at=now() WHERE id=$1`, uid); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось сохранить")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// AgeConfirmed проверяет 18+ (использует T29 перед invoice).
func (s *Service) AgeConfirmed(ctx context.Context, userID string) bool {
	var ok bool
	_ = s.pg.QueryRow(ctx,
		`SELECT age_confirmed_at IS NOT NULL FROM users WHERE id=$1`, userID).Scan(&ok)
	return ok
}
