// Package access — ручная выдача и отзыв доступа (безлимита) владельцу.
//
// Зачем: безлимит в проекте — не флаг «роль админа», а активная строка в
// subscriptions (см. entitlements.go:218). Админ-API умел возвраты, рассылки и
// статистику, но НЕ умел выдать доступ, поэтому это приходилось делать SQL
// руками — и каждый раз упираться в ограничение source_type
// ('legacy'|'payment'). Здесь — единственное место, где это знание живёт.
//
// Что важно понимать про модель:
//   - бесплатные расклады и премиум считаются РАЗНЫМИ ветками лимитов, и
//     активная подписка снимает обе (entitlements.go:218 для премиума,
//     ветка free смотрит туда же);
//   - поэтому «выдать безлимит» = одна подписка, а не две записи;
//   - source_type='legacy' — сознательно: платёжа нет, payment_id NULL, и это
//     отличает ручную выдачу от реальной оплаты в отчётности.
package access

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoPlan — в справочнике планов нет кода, по которому выдаём доступ.
// Это не должно случаться на живом стенде (миграция seeds планы), но если
// случилось — молча выдавать нельзя: оператор должен увидеть, что seeds не
// применены, иначе «выдал доступ» окажется выдумкой.
var ErrNoPlan = errors.New("план не найден в справочнике plans")

// ErrNoUser — аккаунта нет. Частая ошибка: перепутан user_id и anon_uuid.
var ErrNoUser = errors.New("пользователь не найден")

// DefaultPlanCode — годовой безлимит. Отдельный код 'dev_unlimited' не нужен:
// различие только в сроке, а лимиты считает сам факт активной подписки.
const DefaultPlanCode = "year_2490"

// DefaultDays — срок по умолчанию 100 лет. Осознанно «навсегда»: доступ
// владельцу на стенде не должен тихо истекать и ломать проверки; отзыв
// делается явно (Revoke), и он виден в списке.
const DefaultDays = 36500

// Grant — выдать безлимит до validUntil. Повторный вызов продлевает срок до
// максимального из существующего и запрошенного, а не создаёт вторую строку:
// иначе со временем в subscriptions накапливаются дубли и «отозвать»
// становится неоднозначным.
func Grant(ctx context.Context, pg *pgxpool.Pool, userID string, days int) (time.Time, error) {
	if days <= 0 {
		days = DefaultDays
	}
	until := time.Now().Add(time.Duration(days) * 24 * time.Hour)

	var exists bool
	if err := pg.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, userID).Scan(&exists); err != nil {
		return time.Time{}, err
	}
	if !exists {
		return time.Time{}, ErrNoUser
	}

	var planID string
	if err := pg.QueryRow(ctx, `SELECT id FROM plans WHERE code=$1 AND is_active`, DefaultPlanCode).Scan(&planID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, ErrNoPlan
		}
		return time.Time{}, err
	}

	var current time.Time
	var existingID string
	err := pg.QueryRow(ctx,
		`SELECT id::text, valid_until FROM subscriptions
		  WHERE user_id=$1 AND status='active' AND plan_code=$2
		  ORDER BY valid_until DESC LIMIT 1`, userID, DefaultPlanCode).Scan(&existingID, &current)
	switch {
	case err == nil:
		// Строка уже есть — продлеваем её, а не плодим дубликаты.
		if current.After(until) {
			until = current
		}
		if _, err := pg.Exec(ctx, `UPDATE subscriptions SET valid_until=$1 WHERE id=$2`, until, existingID); err != nil {
			return time.Time{}, err
		}
	case errors.Is(err, pgx.ErrNoRows):
		if _, err := pg.Exec(ctx, `
			INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, valid_until, source_type)
			VALUES ($1, $2, $3, 0, $4, 'legacy')`, userID, planID, DefaultPlanCode, until); err != nil {
			return time.Time{}, err
		}
	default:
		return time.Time{}, err
	}
	_, _ = pg.Exec(ctx, `UPDATE users SET updated_at=now() WHERE id=$1`, userID)
	return until, nil
}

// Revoke — отозвать безлимит. Строка остаётся в базе со status='revoked':
// аудит важнее чистоты, а active-подписка всё равно не учитывается.
func Revoke(ctx context.Context, pg *pgxpool.Pool, userID string) (int64, error) {
	tag, err := pg.Exec(ctx,
		`UPDATE subscriptions SET status='revoked'
		  WHERE user_id=$1 AND status='active' AND plan_code=$2`, userID, DefaultPlanCode)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Entry — строка списка доступа.
type Entry struct {
	UserID     string    `json:"user_id"`
	PlanCode   string    `json:"plan_code"`
	Status     string    `json:"status"`
	ValidUntil time.Time `json:"valid_until"`
	Source     string    `json:"source_type"`
	// Active — «доступ действует прямо сейчас». Не хранится в БД: это вывод
	// из status + valid_until, и панель показывает его, чтобы отличать
	// действующий доступ от отозванного или истёкшего.
	Active bool `json:"active"`
}

// List — у кого сейчас есть ручной доступ. Нужен, чтобы оператор мог найти
// себя и убрать доступ, который выдал месяц назад и забыл.
func List(ctx context.Context, pg *pgxpool.Pool) ([]Entry, error) {
	rows, err := pg.Query(ctx, `
		SELECT user_id::text, plan_code, status, valid_until, source_type
		  FROM subscriptions
		 WHERE plan_code=$1 AND source_type='legacy'
		 ORDER BY valid_until DESC LIMIT 200`, DefaultPlanCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.UserID, &e.PlanCode, &e.Status, &e.ValidUntil, &e.Source); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Describe — короткое описание для вывода в терминал.
func (e Entry) Describe() string {
	return fmt.Sprintf("%s  %-9s до %s  (%s)", e.UserID, e.Status, e.ValidUntil.Format("2006-01-02"), e.Source)
}
