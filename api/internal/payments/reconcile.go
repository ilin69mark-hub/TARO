package payments

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A09/F-09: payment_webhook_events писалась, но её никто не читал. Дубликаты
// списаний и расхождения вебхуков фиксировались в таблице без единого читателя:
// duplicate_charge, charge_reuse, owner/amount_mismatch оставались незамеченными.
// Ниже — единственная выборка, которой пользуются adminctl и админ-API.

// WebhookMismatch — один платёж с причиной расхождения, сгруппированный.
type WebhookMismatch struct {
	PaymentID     string    `json:"payment_id"`
	UserID        string    `json:"user_id"`
	Reason        string    `json:"reason"`
	Events        int       `json:"events"`
	PaymentStatus string    `json:"payment_status"`
	RefundState   string    `json:"refund_state"`
	AmountRub     int       `json:"amount_rub"`
	Stars         int       `json:"stars"`
	Currency      string    `json:"currency"`
	FirstSeen     time.Time `json:"first_seen"`
	LastSeen      time.Time `json:"last_seen"`
}

// NeedsAttention — причина, по которой запись должна быть разобрана вручную.
// owner_unverified — это нормальный первый вебхук без привязанного Telegram,
// он не является инцидентом.
func (m WebhookMismatch) NeedsAttention() bool {
	return m.Reason != ReasonOwnerUnverified
}

// Причины расхождений. Литералы в payments.go заменены на эти константы,
// поэтому список не может разойтись с кодом (A09/F-09).
const (
	ReasonAmountMismatch  = "amount_mismatch"
	ReasonChargeMismatch  = "charge_mismatch"
	ReasonOwnerMismatch   = "owner_mismatch"
	ReasonChargeReused    = "charge_reused"
	ReasonOwnerUnverified = "owner_unverified"
	ReasonDuplicateCharge = "duplicate_charge"
	ReasonRefundingPay    = "refunding_payment"
)

// ReconcileWebhookEvents возвращает платежи с расхождениями, зафиксированными
// позже since, сгруппированными по (payment_id, reason). Строки без
// payment_id невозможны (NOT NULL), но платёж мог быть удалён — такие строки
// теряем осознанно: они и есть «хвост» без владельца.
func ReconcileWebhookEvents(ctx context.Context, pg *pgxpool.Pool, since time.Time, limit int) ([]WebhookMismatch, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := pg.Query(ctx, `
		SELECT e.payment_id::text, COALESCE(p.user_id::text, ''), e.reason,
		       count(*)::int,
		       COALESCE(p.status, ''), COALESCE(p.refund_state, ''),
		       COALESCE(p.amount_rub, 0), COALESCE(p.stars, 0),
		       max(e.currency), min(e.created_at), max(e.created_at)
		  FROM payment_webhook_events e
		  LEFT JOIN payments p ON p.id = e.payment_id
		 WHERE e.created_at >= $1
		 GROUP BY e.payment_id, e.reason, p.user_id, p.status, p.refund_state, p.amount_rub, p.stars
		 ORDER BY max(e.created_at) DESC
		 LIMIT $2`, since, limit)
	if err != nil {
		return nil, fmt.Errorf("query webhook events: %w", err)
	}
	defer rows.Close()
	var out []WebhookMismatch
	for rows.Next() {
		var m WebhookMismatch
		if err := rows.Scan(&m.PaymentID, &m.UserID, &m.Reason, &m.Events,
			&m.PaymentStatus, &m.RefundState, &m.AmountRub, &m.Stars,
			&m.Currency, &m.FirstSeen, &m.LastSeen); err != nil {
			return nil, fmt.Errorf("scan webhook event: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("webhook events rows: %w", err)
	}
	return out, nil
}

// WebhookEventSummary — короткая сводка для вывода в CLI и алерта.
func WebhookEventSummary(items []WebhookMismatch) (attention int, byReason map[string]int) {
	byReason = map[string]int{}
	for _, m := range items {
		byReason[m.Reason]++
		if m.NeedsAttention() {
			attention++
		}
	}
	return attention, byReason
}
