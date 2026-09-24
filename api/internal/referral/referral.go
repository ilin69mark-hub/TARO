// Package referral — рефералка v1 (см. docs/project-book/02-functional/06, T14).
// Флоу: apply {code} до 1-го reading → pending; хук после 1-го reading → проверки → completed.
// Бонус +3д обоим в subscriptions (plan referral_bonus), только при referee.tg_id NOT NULL.
// Капы: 30д/мес (entitlements.referral_bonus_month) + lifetime-счетчик.
package referral

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"taro/api/internal/apierr"
	"taro/api/internal/auth"
	"taro/api/internal/entitlements"
)

// grantBonusDaysTx — то же, что entitlements.GrantBonusDays, но внутри tx (атомарно с completed).
func grantBonusDaysTx(ctx context.Context, tx pgx.Tx, userID, planCode string, days int) error {
	var planID string
	if err := tx.QueryRow(ctx,
		`SELECT id FROM plans WHERE code=$1 AND is_active ORDER BY valid_from DESC LIMIT 1`, planCode).Scan(&planID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, valid_until)
		 SELECT $1,$2,$3,0, GREATEST(COALESCE(MAX(valid_until), now()), now()) + make_interval(days => $4)
		   FROM subscriptions WHERE user_id=$1 AND status='active'`,
		userID, planID, planCode, days)
	return err
}

const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // без похожих символов

// Service — рефералка.
type Service struct {
	pg *pgxpool.Pool
	en *entitlements.Service
}

// New возвращает сервис.
func New(pg *pgxpool.Pool, en *entitlements.Service) *Service {
	return &Service{pg: pg, en: en}
}

// genCode — случайный код 8 символов.
func genCode() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	out := make([]byte, 8)
	for i, v := range b {
		out[i] = alphabet[int(v)%len(alphabet)]
	}
	return string(out)
}

// myCode возвращает или создает код юзера (колонка users.referral_code, см. D-баг 014).
func (s *Service) myCode(ctx context.Context, userID string) (string, error) {
	var code *string
	if err := s.pg.QueryRow(ctx,
		`SELECT referral_code FROM users WHERE id=$1`, userID).Scan(&code); err != nil {
		return "", err
	}
	if code != nil {
		return *code, nil
	}
	for range [5]struct{}{} {
		code := genCode()
		var got string
		err := s.pg.QueryRow(ctx,
			`UPDATE users SET referral_code=$1 WHERE id=$2 AND referral_code IS NULL RETURNING referral_code`,
			code, userID).Scan(&got)
		if err == nil {
			return got, nil
		}
	}
	return "", fmt.Errorf("code collision")
}

// HandleMe — GET /v1/referral/me: {code, invited, bonus_days}.
func (s *Service) HandleMe(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	ctx := r.Context()
	code, err := s.myCode(ctx, uid)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось получить код")
		return
	}
	var invited, bonus int
	_ = s.pg.QueryRow(ctx,
		`SELECT COUNT(*) FROM referrals WHERE referrer_id=$1 AND status='completed'`, uid).Scan(&invited)
	_ = s.pg.QueryRow(ctx,
		`SELECT COALESCE(SUM(bonus_days),0) FROM referrals WHERE (referrer_id=$1 OR referee_id=$1) AND status='completed'`, uid).Scan(&bonus)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "invited": invited, "bonus_days": bonus})
}

// HandleApply — POST /v1/referral/apply {code} до 1-го reading → pending.
func (s *Service) HandleApply(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	var req struct {
		Code string `json:"code"`
	}
	if !apierr.Decode(w, r, &req) || req.Code == "" || len(req.Code) > 16 || strings.ContainsRune(req.Code, 0) {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нужен код")
		return
	}
	ctx := r.Context()
	var referrer string
	err := s.pg.QueryRow(ctx,
		`SELECT id FROM users WHERE referral_code=$1`, req.Code).Scan(&referrer)
	if err != nil {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Неверный код")
		return
	}
	if referrer == uid {
		apierr.Write(w, http.StatusConflict, "SELF", "Свой код применить нельзя")
		return
	}
	// уже есть чтение? apply только до 1-го
	var readings int
	_ = s.pg.QueryRow(ctx, `SELECT COUNT(*) FROM readings WHERE user_id=$1`, uid).Scan(&readings)
	if readings > 0 {
		apierr.Write(w, http.StatusConflict, "ALREADY_REFERRED", "Бонус только для новичков")
		return
	}
	_, err = s.pg.Exec(ctx,
		`INSERT INTO referrals (referrer_id, referee_id, code, status, bonus_days)
		 VALUES ($1,$2,$3,'pending',3)
		 ON CONFLICT (referee_id) DO NOTHING`, referrer, uid, genCode())
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось применить код")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"applied": "pending"})
}

// CompleteOnFirstReading — хук после 1-го done-чтения (вызывать горутиной, не блокирует).
// completed только при referee.tg_id NOT NULL + кап referrer 30д/мес.
func (s *Service) CompleteOnFirstReading(ctx context.Context, refereeID string) {
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	var refID, referrer string
	var bonus int
	err = tx.QueryRow(ctx,
		`SELECT id, referrer_id, bonus_days FROM referrals
		  WHERE referee_id=$1 AND status='pending' FOR UPDATE`, refereeID).Scan(&refID, &referrer, &bonus)
	if err != nil {
		return // apply не было — нечего завершать
	}
	if referrer == refereeID {
		// Аудит D: self-referral (артефакт мержа) — бонуса нет, помечаем rejected.
		_, _ = tx.Exec(ctx, `UPDATE referrals SET status='rejected' WHERE id=$1 AND status='pending'`, refID)
		_ = tx.Commit(ctx)
		return
	}
	var tg *int64
	_ = tx.QueryRow(ctx, `SELECT tg_id FROM users WHERE id=$1`, refereeID).Scan(&tg)
	if tg == nil {
		_, _ = tx.Exec(ctx, `UPDATE referrals SET status='rejected' WHERE id=$1 AND status='pending'`, refID)
		_ = tx.Commit(ctx)
		return // anon-ферма отрезана (см. 02-functional/06)
	}
	// кап referrer: 30д/календарный месяц
	monthKey := time.Now().Format("2006-01")
	var monthUsed int
	_ = tx.QueryRow(ctx,
		`SELECT referral_bonus_month FROM entitlements WHERE user_id=$1 AND referral_bonus_month_key=$2`,
		referrer, monthKey).Scan(&monthUsed)
	if monthUsed+bonus > 30 {
		_, _ = tx.Exec(ctx, `UPDATE referrals SET status='rejected' WHERE id=$1 AND status='pending'`, refID)
		_ = tx.Commit(ctx)
		return
	}
	if err := grantBonusDaysTx(ctx, tx, referrer, "referral_bonus", bonus); err != nil {
		return
	}
	if err := grantBonusDaysTx(ctx, tx, refereeID, "referral_bonus", bonus); err != nil {
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE referrals SET status='completed' WHERE id=$1 AND status='pending'`, refID); err != nil {
		return
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO entitlements (user_id, referral_bonus_month, referral_bonus_month_key, referral_bonus_lifetime)
		VALUES ($1,$2,$3,$2) ON CONFLICT (user_id) DO UPDATE SET
		  referral_bonus_month = CASE WHEN entitlements.referral_bonus_month_key=$3
		    THEN entitlements.referral_bonus_month + $2 ELSE $2 END,
		  referral_bonus_month_key = $3,
		  referral_bonus_lifetime = entitlements.referral_bonus_lifetime + $2`, referrer, bonus, monthKey); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}
