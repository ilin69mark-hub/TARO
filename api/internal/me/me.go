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

// HandleDelete — DELETE /v1/me {"confirm": user_id}: стирает все + гасит cookie.
// Аудит D: confirm обязателен (один CSRF-запрос больше не удаляет необратимо).
func (s *Service) HandleDelete(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	var req struct {
		Confirm string `json:"confirm"`
	}
	// Аудит D: confirm="DELETE" обязателен (защита от случайного/повторного вызова;
	// от CSRF защищает X-CSRF-токен, здесь — от необратимости по ошибке).
	if !apierr.Decode(w, r, &req) || req.Confirm != "DELETE" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нужно подтверждение удаления")
		return
	}
	ctx := r.Context()
	// Redis: sess + csrf + admin-sess + все ent/love ключи юзера (SCAN, не KEYS)
	_ = s.rd.Del(ctx, "sess:"+uid, "csrf:"+uid, "sess:admin:"+uid).Err()
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
	// admin_audit, reading_quota_quarantine_034 и payment_webhook_events без CASCADE —
	// чистим явно в одной транзакции, иначе после DELETE users остаются строки юзера.
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось удалить данные")
		return
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `DELETE FROM admin_audit WHERE admin_id=$1`, uid); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось удалить данные")
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM reading_quota_quarantine_034 WHERE user_id=$1`, uid); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось удалить данные")
		return
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM payment_webhook_events
		 WHERE payment_id IN (SELECT id FROM payments WHERE user_id=$1)`, uid); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось удалить данные")
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id=$1`, uid); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось удалить данные")
		return
	}
	if err := tx.Commit(ctx); err != nil {
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
	if !apierr.Decode(w, r, &req) || !req.Confirmed {
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
