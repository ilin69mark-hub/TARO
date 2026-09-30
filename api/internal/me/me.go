// Package me — профиль, удаление данных и age-gate (см. docs/project-book/08-risks/02, T15).
// GET /v1/me: личность текущей сессии. Отдельный read-эндпоинт, которого в проекте
// не было вообще, и из-за этого нельзя было ни показать человеку его user_id,
// ни дать оператору найти аккаунт для выдачи доступа (см. 02-functional/02).
// POST /v1/auth/link закрывает дыру с анонимной оплатой, но без /v1/me баннер
// «привяжите Telegram» не знает, что человек аноним.
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
	"time"

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
	// Redis (sess + csrf + admin-sess + все ent/love ключи, SCAN вместо KEYS)
	// чистим ТОЛЬКО после коммита: раньше транзакция удаления могла откатиться
	// (например, 40P01 из-за конкурентного вебхука), а сессия и квоты уже были
	// уничтожены — пользователь получал 500 и принудительный повторный логин
	// поверх живой учётной записи (A08/F-08).
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
	secure, sameSite := auth.CookieFlags(r)
	http.SetCookie(w, &http.Cookie{
		Name: auth.CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: sameSite,
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

// Profile — ответ GET /v1/me.
type Profile struct {
	UserID         string     `json:"user_id"`
	TelegramLinked bool       `json:"telegram_linked"`
	AgeConfirmed   bool       `json:"age_confirmed"`
	HasPayment     bool       `json:"has_payment"`
	ValidUntil     *time.Time `json:"valid_until"`
	CreatedAt      time.Time  `json:"created_at"`
	ReferralCode   string     `json:"referral_code"`
	ReadingsTotal  int        `json:"readings_total"`
	// LastPlanCode — тариф последней действующей покупки. Нужен не для показа,
	// а для переноса: в токен кладётся plan_code, и без него клиент вынужден
	// слать пустую строку, а пустой plan_code в аудите переноса ничего не
	// говорит. NULL, если активной подписки нет.
	LastPlanCode *string `json:"last_plan_code"`
}

// HandleGet — GET /v1/me: личность текущей сессии.
//
// Что здесь и чего здесь НЕ быть должно. Квоты (free_left, love_left_week,
// winback) считает GET /v1/entitlements/me — с кэшем в Redis и правилом
// winback; дублировать их здесь значит завести второй источник правды по
// лимитам, который разойдётся с первым при первом же изменении. Поэтому
// здесь только личность и признаки, по которым фронт решает, что показать.
//
// has_payment — не «есть подписка», а «когда-либо платил». Разница важна:
// подписка могла истечь, а человек мог заплатить и об этом не знает, и ему
// по-прежнему показывают предложение купить то, что он уже купил. И это же
// условие для баннера «привяжите Telegram»: аноним, который что-то оплатил,
// теряет деньги при смене устройства.
func (s *Service) HandleGet(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	var (
		p          Profile
		tgID       *int64
		ageOK      bool
		refCode    *string
		validUntil *time.Time
		hasPayment bool
	)
	// pg_advisory не нужен: чтение своего профиля не конфликтует ни с чем.
	err := s.pg.QueryRow(r.Context(), `
		SELECT tg_id, age_confirmed_at IS NOT NULL, referral_code, created_at,
		       EXISTS(SELECT 1 FROM payments WHERE user_id=$1 AND status='succeeded')
		  FROM users WHERE id=$1`, uid).
		Scan(&tgID, &ageOK, &refCode, &p.CreatedAt, &hasPayment)
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Не удалось загрузить профиль")
		return
	}
	_ = s.pg.QueryRow(r.Context(),
		`SELECT MAX(valid_until) FROM subscriptions
		  WHERE user_id=$1 AND status='active' AND valid_until > now()`, uid).Scan(&validUntil)
	_ = s.pg.QueryRow(r.Context(),
		`SELECT count(*) FROM readings WHERE user_id=$1`, uid).Scan(&p.ReadingsTotal)
	// plan_code берём из строки с самой дальнейшей подпиской, а не из
	// «последней оплаченной»: перенос открывает счёт, и счёт должен
	// соответствовать тому, что сейчас действует, а не тому, что человек
	// когда-то покупал.
	var lastPlan *string
	if err := s.pg.QueryRow(r.Context(),
		`SELECT plan_code FROM subscriptions
		  WHERE user_id=$1 AND status='active' AND valid_until > now()
		  ORDER BY valid_until DESC LIMIT 1`, uid).Scan(&lastPlan); err == nil {
		p.LastPlanCode = lastPlan
	}

	p.UserID = uid
	p.TelegramLinked = tgID != nil
	p.AgeConfirmed = ageOK
	p.HasPayment = hasPayment
	p.ValidUntil = validUntil
	if refCode != nil {
		p.ReferralCode = *refCode
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(p)
}
