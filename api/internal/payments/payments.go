package payments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"taro/api/internal/apierr"
	"taro/api/internal/auth"
	"taro/api/internal/me"
)

type Service struct {
	pg   *pgxpool.Pool
	mv   *me.Service
	http *http.Client
}

func New(pg *pgxpool.Pool, mv *me.Service) *Service {
	return &Service{pg: pg, mv: mv, http: &http.Client{Timeout: 10 * time.Second}}
}

type tgCallError struct {
	message   string
	ambiguous bool
}

func (e *tgCallError) Error() string { return e.message }

func (s *Service) tgCall(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	token := os.Getenv("TG_BOT_TOKEN")
	if token == "" || token == "dev-only-bot" {
		return nil, &tgCallError{message: "no bot token"}
	}
	body, err := json.Marshal(params)
	if err != nil {
		return nil, &tgCallError{message: "invalid telegram request", ambiguous: true}
	}
	req, err := http.NewRequestWithContext(ctx,
		"POST", "https://api.telegram.org/bot"+token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return nil, &tgCallError{message: "invalid telegram request", ambiguous: true}
	}
	req.Header.Set("Content-Type", "application/json")
	client := s.http
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &tgCallError{message: "telegram request failed", ambiguous: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, &tgCallError{message: "telegram request failed", ambiguous: true}
	}
	var out struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Desc   string          `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, &tgCallError{message: "invalid telegram response", ambiguous: true}
	}
	if !out.OK {
		return nil, &tgCallError{message: "tg api: " + out.Desc}
	}
	return out.Result, nil
}

func tgTokenReady() bool {
	token := os.Getenv("TG_BOT_TOKEN")
	return token != "" && token != "dev-only-bot"
}

type planSnapshot struct {
	id       string
	price    int
	stars    int
	duration *int
}

type storedPayment struct {
	ID                   string
	UserID               string
	PlanID               string
	PlanCode             string
	Provider             string
	ProviderPaymentID    string
	Price                int
	Amount               int
	Stars                int
	Status               string
	PurchaseFingerprint  string
	Duration             *int
	TelegramCharge       *string
	ProviderCharge       *string
	RefundState          string
	ReconciliationReason *string
}

type webhookPayment struct {
	storedPayment
	ownerTG *int64
}

type refundPayment struct {
	storedPayment
	refundTableState string
	ownerTG          *int64
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

const storedPaymentColumns = `p.id::text, p.user_id::text, p.plan_id::text, p.plan_code,
	p.price_rub_snapshot, p.provider, p.provider_payment_id, p.amount_rub, p.stars,
	p.status, COALESCE(p.purchase_fingerprint, ''), COALESCE(p.duration_days_snapshot, pl.duration_days),
	p.telegram_payment_charge_id, p.provider_payment_charge_id, COALESCE(p.refund_state, 'none'),
	p.reconciliation_reason`

func paymentScanDest(p *storedPayment) []any {
	return []any{
		&p.ID, &p.UserID, &p.PlanID, &p.PlanCode, &p.Price, &p.Provider,
		&p.ProviderPaymentID, &p.Amount, &p.Stars, &p.Status, &p.PurchaseFingerprint,
		&p.Duration, &p.TelegramCharge, &p.ProviderCharge, &p.RefundState, &p.ReconciliationReason,
	}
}

func (s *Service) loadPaymentByKey(ctx context.Context, userID, key string) (storedPayment, error) {
	var p storedPayment
	err := s.pg.QueryRow(ctx, `SELECT `+storedPaymentColumns+`
		FROM payments p LEFT JOIN plans pl ON pl.id=p.plan_id
		WHERE p.user_id=$1 AND p.idempotency_key=$2`, userID, key).Scan(paymentScanDest(&p)...)
	return p, err
}

func (s *Service) loadWebhookPayment(ctx context.Context, q queryRower, id string) (webhookPayment, error) {
	var p webhookPayment
	dest := paymentScanDest(&p.storedPayment)
	dest = append(dest, &p.ownerTG)
	err := q.QueryRow(ctx, `SELECT `+storedPaymentColumns+`, u.tg_id
		FROM payments p
		LEFT JOIN plans pl ON pl.id=p.plan_id
		LEFT JOIN users u ON u.id=p.user_id
		WHERE p.id=$1 FOR UPDATE OF p`, id).Scan(dest...)
	return p, err
}

func (s *Service) loadRefundPayment(ctx context.Context, q queryRower, id string, lock bool) (refundPayment, error) {
	var p refundPayment
	dest := paymentScanDest(&p.storedPayment)
	dest = append(dest, &p.refundTableState, &p.ownerTG)
	query := `SELECT ` + storedPaymentColumns + `, COALESCE(pr.state, ''), u.tg_id
		FROM payments p
		LEFT JOIN plans pl ON pl.id=p.plan_id
		LEFT JOIN payment_refunds pr ON pr.payment_id=p.id
		LEFT JOIN users u ON u.id=p.user_id
		WHERE p.id=$1`
	if lock {
		query += ` FOR UPDATE OF p`
	}
	err := q.QueryRow(ctx, query, id).Scan(dest...)
	return p, err
}

func purchaseFingerprint(planCode string, price, stars int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", planCode, price, stars)))
	return hex.EncodeToString(sum[:])
}

func (s *Service) planFor(ctx context.Context, code string) (planSnapshot, error) {
	var p planSnapshot
	err := s.pg.QueryRow(ctx, `SELECT id::text, price_rub, stars_amount, duration_days
		FROM (
			SELECT DISTINCT ON (code) id, code, price_rub, stars_amount, duration_days, is_active
			FROM plans WHERE code=$1
			ORDER BY code, valid_from DESC
		) latest
		WHERE latest.is_active AND latest.code IN ('month_299','year_2490','single_99')`, code).Scan(&p.id, &p.price, &p.stars, &p.duration)
	return p, err
}

func (s *Service) effectivePrice(ctx context.Context, userID, code string, base int) (int, string) {
	if code != "month_299" {
		return base, ""
	}
	price := base
	if _, abPrice := s.VariantFor(ctx, userID); abPrice > 0 {
		price = abPrice
	}
	if discount, pct := s.winbackPrice(ctx, userID, price); discount > 0 {
		return discount, "winback-" + itoa(pct)
	}
	return price, ""
}

func writeStoredPayment(w http.ResponseWriter, p storedPayment, link string) {
	out := map[string]any{"payment_id": p.ID, "status": p.Status}
	if link != "" {
		out["invoice_link"] = link
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func writeIdempotencyConflict(w http.ResponseWriter) {
	apierr.Write(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Ключ уже привязан к другому тарифу или сумме")
}

func (s *Service) useExistingInvoice(w http.ResponseWriter, r *http.Request, uid string, p storedPayment, requestedPlan string) {
	if p.PlanCode != requestedPlan {
		writeIdempotencyConflict(w)
		return
	}
	if p.Status != "pending" {
		writeStoredPayment(w, p, "")
		return
	}
	plan, err := s.planFor(r.Context(), requestedPlan)
	if err == nil {
		price, _ := s.effectivePrice(r.Context(), uid, requestedPlan, plan.price)
		fingerprint := purchaseFingerprint(requestedPlan, price, plan.stars)
		if p.PurchaseFingerprint != "" && p.PurchaseFingerprint != fingerprint {
			writeIdempotencyConflict(w)
			return
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Платежи временно недоступны")
		return
	}
	link, err := StarsProvider{}.CreateInvoice(r.Context(), s.http, p.ID, p.PlanCode, p.Stars, p.Price)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Платежи временно недоступны")
		return
	}
	writeStoredPayment(w, p, link)
}

func (s *Service) HandleInvoice(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	if !s.mv.AgeConfirmed(r.Context(), uid) {
		apierr.Write(w, http.StatusForbidden, apierr.CodeForbidden, "Подтверди 18+ в профиле")
		return
	}
	var req struct {
		PlanCode       string `json:"plan_code"`
		Provider       string `json:"provider"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if !apierr.Decode(w, r, &req) || req.PlanCode == "" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нужен plan_code")
		return
	}
	if req.IdempotencyKey == "" || len(req.IdempotencyKey) > 64 || strings.TrimSpace(req.IdempotencyKey) == "" || !validOpaqueID(req.IdempotencyKey) {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нужен idempotency_key (1..64)")
		return
	}
	prov := req.Provider
	if prov == "" {
		prov = "tg_stars"
	}
	if prov == "yookassa" {
		apierr.Write(w, http.StatusNotImplemented, "YOOKASSA_DISABLED", "Оплата картой скоро: проходим KYC")
		return
	}
	if prov != "tg_stars" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Неизвестный провайдер")
		return
	}

	ctx := r.Context()
	existing, err := s.loadPaymentByKey(ctx, uid, req.IdempotencyKey)
	if err == nil {
		s.useExistingInvoice(w, r, uid, existing, req.PlanCode)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Платежи временно недоступны")
		return
	}

	plan, err := s.planFor(ctx, req.PlanCode)
	if errors.Is(err, pgx.ErrNoRows) {
		apierr.Write(w, http.StatusUnprocessableEntity, "UNKNOWN_PLAN", "Неизвестный тариф")
		return
	}
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Платежи временно недоступны")
		return
	}
	price, note := s.effectivePrice(ctx, uid, req.PlanCode, plan.price)
	if price <= 0 || plan.stars <= 0 || price > 1000000 || plan.stars > 1000000 {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Платежи временно недоступны")
		return
	}
	fingerprint := purchaseFingerprint(req.PlanCode, price, plan.stars)
	var paymentID string
	err = s.pg.QueryRow(ctx, `
		INSERT INTO payments
		(user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id,
		 amount_rub, stars, status, note, idempotency_key, purchase_fingerprint, duration_days_snapshot)
		VALUES ($1,$2,$3,$4,'tg_stars','pending:'||gen_random_uuid(),$4,$5,'pending',NULLIF($6,''),$7,$8,$9)
		ON CONFLICT (user_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
		RETURNING id::text`, uid, plan.id, req.PlanCode, price, plan.stars, note, req.IdempotencyKey, fingerprint, plan.duration).Scan(&paymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, findErr := s.loadPaymentByKey(ctx, uid, req.IdempotencyKey)
		if findErr != nil {
			apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать платеж")
			return
		}
		s.useExistingInvoice(w, r, uid, existing, req.PlanCode)
		return
	}
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать платеж")
		return
	}
	link, err := StarsProvider{}.CreateInvoice(ctx, s.http, paymentID, req.PlanCode, plan.stars, price)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Платежи временно недоступны")
		return
	}
	writeStoredPayment(w, storedPayment{
		ID:                  paymentID,
		PlanCode:            req.PlanCode,
		Price:               price,
		Stars:               plan.stars,
		Status:              "pending",
		PurchaseFingerprint: fingerprint,
	}, link)
}

func (s *Service) VariantFor(ctx context.Context, userID string) (variant string, price int) {
	var raw json.RawMessage
	if err := s.pg.QueryRow(ctx, `SELECT value FROM app_config WHERE key='ab.price_month'`).Scan(&raw); err != nil {
		return "control", 0
	}
	var cfg struct {
		Enabled bool `json:"enabled"`
		Control int  `json:"control"`
		Test    int  `json:"test"`
		Split   int  `json:"split"`
	}
	if json.Unmarshal(raw, &cfg) != nil || !cfg.Enabled {
		return "control", 0
	}
	sum := sha256.Sum256([]byte(userID))
	if int(sum[0])*100/256 < cfg.Split {
		return "test", cfg.Test
	}
	return "control", cfg.Control
}

func (s *Service) HandleVariant(w http.ResponseWriter, r *http.Request) {
	variant, price := s.VariantFor(r.Context(), auth.UserID(r.Context()))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"variant": variant, "price_rub": price})
}

func (s *Service) winbackPrice(ctx context.Context, userID string, base int) (int, int) {
	var raw json.RawMessage
	if err := s.pg.QueryRow(ctx, `SELECT value FROM app_config WHERE key='offers.winback'`).Scan(&raw); err != nil {
		return 0, 0
	}
	var cfg struct {
		Enabled bool `json:"enabled"`
		Pct     int  `json:"pct"`
	}
	if json.Unmarshal(raw, &cfg) != nil || !cfg.Enabled || cfg.Pct <= 0 || cfg.Pct >= 100 {
		return 0, 0
	}
	var eligible bool
	_ = s.pg.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM subscriptions WHERE user_id=$1 AND status IN ('active','expired')
		  AND valid_until < now() - interval '14 days')
		   AND NOT EXISTS(SELECT 1 FROM subscriptions WHERE user_id=$2 AND status='active' AND valid_until > now())`,
		userID, userID).Scan(&eligible)
	if !eligible {
		return 0, 0
	}
	return base * (100 - cfg.Pct) / 100, cfg.Pct
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

type successfulPayment struct {
	Currency              string `json:"currency"`
	TotalAmount           int    `json:"total_amount"`
	InvoicePayload        string `json:"invoice_payload"`
	TelegramPaymentCharge string `json:"telegram_payment_charge_id"`
	ProviderPaymentCharge string `json:"provider_payment_charge_id"`
}

type tgUpdate struct {
	Message *struct {
		SuccessfulPayment *successfulPayment `json:"successful_payment"`
		From              *struct {
			ID int64 `json:"id"`
		} `json:"from"`
	} `json:"message"`
}

func validUUID(s string) bool {
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return false
	}
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func validOpaqueID(s string) bool {
	if s == "" || len(s) > 128 || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func writeWebhookIgnored(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ignored": reason})
}

func writeWebhookDuplicate(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "duplicate": true})
}

func writeWebhookReconciliation(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "reconciliation_required": true})
}

func webhookReconciliationCanProceed(p webhookPayment) bool {
	if p.ReconciliationReason == nil {
		return false
	}
	switch *p.ReconciliationReason {
	case "amount_mismatch", "charge_mismatch", "owner_mismatch", "charge_reused":
		return true
	case "owner_unverified":
		return p.ownerTG != nil && *p.ownerTG > 0
	default:
		return false
	}
}

type webhookIdentity struct {
	ownerTG        int64
	telegramCharge string
	providerCharge string
}

func (s *Service) originalOwnerUnverifiedEvent(ctx context.Context, q queryRower, paymentID string) (webhookIdentity, bool, error) {
	var identity webhookIdentity
	err := q.QueryRow(ctx, `SELECT owner_tg_id, telegram_charge_id, provider_charge_id
		FROM payment_webhook_events
		WHERE payment_id=$1 AND reason='owner_unverified'
		ORDER BY created_at ASC, id ASC
		LIMIT 1`, paymentID).Scan(&identity.ownerTG, &identity.telegramCharge, &identity.providerCharge)
	if errors.Is(err, pgx.ErrNoRows) {
		return webhookIdentity{}, false, nil
	}
	if err != nil {
		return webhookIdentity{}, false, err
	}
	return identity, true, nil
}

func webhookIdentityMatches(identity webhookIdentity, sp successfulPayment, ownerTG int64) bool {
	return identity.ownerTG == ownerTG &&
		identity.telegramCharge == sp.TelegramPaymentCharge &&
		identity.providerCharge == sp.ProviderPaymentCharge
}

func (s *Service) webhookChargeReused(ctx context.Context, q queryRower, paymentID, telegramCharge, providerCharge string) (bool, error) {
	var reused bool
	err := q.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM payments WHERE provider='tg_stars' AND id<>$1
		  AND (telegram_payment_charge_id=$2 OR provider_payment_charge_id=$3 OR provider_payment_id='tg:'||$2))`,
		paymentID, telegramCharge, providerCharge).Scan(&reused)
	return reused, err
}

func webhookEventHash(sp successfulPayment, ownerTG int64) string {
	fields := []string{sp.Currency, strconv.Itoa(sp.TotalAmount), sp.InvoicePayload, sp.TelegramPaymentCharge, sp.ProviderPaymentCharge, strconv.FormatInt(ownerTG, 10)}
	encoded := make([]byte, 0, 128)
	for _, field := range fields {
		encoded = strconv.AppendInt(encoded, int64(len(field)), 10)
		encoded = append(encoded, ':')
		encoded = append(encoded, field...)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (s *Service) recordWebhookEvent(ctx context.Context, tx pgx.Tx, p webhookPayment, sp successfulPayment, ownerTG int64, reason string) (bool, error) {
	tag, err := tx.Exec(ctx, `INSERT INTO payment_webhook_events
		(payment_id, event_hash, reason, currency, total_amount, telegram_charge_id, provider_charge_id, owner_tg_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (payment_id, event_hash) DO NOTHING`, p.ID, webhookEventHash(sp, ownerTG), reason, sp.Currency, sp.TotalAmount, sp.TelegramPaymentCharge, sp.ProviderPaymentCharge, ownerTG)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Service) markWebhookMismatch(ctx context.Context, tx pgx.Tx, p webhookPayment, sp successfulPayment, ownerTG int64, reason string, reconcile bool) error {
	inserted, err := s.recordWebhookEvent(ctx, tx, p, sp, ownerTG, reason)
	if err != nil || !inserted {
		return err
	}
	if reconcile {
		_, err = tx.Exec(ctx, `UPDATE payments
			SET reconciliation_reason=CASE
					WHEN status='reconciliation' AND reconciliation_reason='owner_unverified' AND $2<>'charge_reused' THEN reconciliation_reason
					ELSE $2
				END,
				note=CASE WHEN NULLIF(btrim(note), '') IS NULL THEN $3 ELSE note || ' ' || $3 END
			WHERE id=$1 AND status IN ('pending','expired','reconciliation')`, p.ID, reason, "payment-reconciliation:"+reason)
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE payments
		SET reconciliation_reason=CASE
				WHEN status IN ('reconciliation','refunding') AND reconciliation_reason IS NOT NULL THEN reconciliation_reason
				ELSE $2
			END,
			note=CASE WHEN NULLIF(btrim(note), '') IS NULL THEN $3 ELSE note || ' ' || $3 END
		WHERE id=$1`, p.ID, reason, "payment-webhook:"+reason)
	return err
}

func (s *Service) markOwnerUnverifiedReconciliation(ctx context.Context, tx pgx.Tx, p webhookPayment, sp successfulPayment, ownerTG int64, stateReason string, storeCharge bool) error {
	inserted, err := s.recordWebhookEvent(ctx, tx, p, sp, ownerTG, "owner_unverified")
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE payments
		SET status='reconciliation', reconciliation_reason=$2,
			note=CASE WHEN $3 THEN CASE WHEN NULLIF(btrim(note), '') IS NULL THEN $4 ELSE note || ' ' || $4 END ELSE note END
		WHERE id=$1 AND status IN ('pending','expired','reconciliation')`, p.ID, stateReason, inserted, "payment-reconciliation:"+stateReason)
	if err != nil || !storeCharge {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE payments
		SET provider_payment_id='tg:'||$2, telegram_payment_charge_id=$2,
			provider_payment_charge_id=$3
		WHERE id=$1 AND status IN ('pending','expired','reconciliation')
		  AND NOT EXISTS (
			SELECT 1 FROM payments other
			 WHERE other.id<>$1 AND other.provider='tg_stars'
			   AND (other.telegram_payment_charge_id=$2 OR other.provider_payment_charge_id=$3 OR other.provider_payment_id='tg:'||$2)
		  )`, p.ID, sp.TelegramPaymentCharge, sp.ProviderPaymentCharge)
	return err
}

func (s *Service) grantPaymentEntitlements(ctx context.Context, tx pgx.Tx, p storedPayment) error {
	if p.PlanCode == "single_99" {
		_, err := tx.Exec(ctx, `INSERT INTO single_entitlements (user_id, spread_code, payment_id)
			VALUES ($1,'any',$2) ON CONFLICT (payment_id) DO NOTHING`, p.UserID, p.ID)
		return err
	}
	days := 30
	if p.Duration != nil && *p.Duration > 0 {
		days = *p.Duration
	}
	_, err := tx.Exec(ctx, `INSERT INTO subscriptions
		(user_id, plan_id, plan_code, price_rub_snapshot, valid_until, payment_id, source_type)
		SELECT $1,$2,$3,$4,
			GREATEST(COALESCE(MAX(valid_until), now()), now()) + make_interval(days => $6), $5, 'payment'
		FROM subscriptions WHERE user_id=$1 AND status='active'`,
		p.UserID, p.PlanID, p.PlanCode, p.Price, p.ID, days)
	return err
}

func (s *Service) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	secret := os.Getenv("TG_STARS_SECRET_TOKEN")
	got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if secret == "" || secret == "dev-only-stars" || len(got) != len(secret) ||
		subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
		apierr.Write(w, http.StatusUnauthorized, apierr.CodeBadSign, "Bad secret token")
		return
	}
	var upd tgUpdate
	r.Body = http.MaxBytesReader(w, r.Body, apierr.MaxBody)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&upd); err != nil || upd.Message == nil || upd.Message.SuccessfulPayment == nil {
		writeWebhookIgnored(w, "malformed")
		return
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		writeWebhookIgnored(w, "malformed")
		return
	}
	sp := upd.Message.SuccessfulPayment
	if sp.Currency != "XTR" || sp.TotalAmount <= 0 || sp.TotalAmount > 1000000 ||
		!validUUID(sp.InvoicePayload) || !validOpaqueID(sp.TelegramPaymentCharge) ||
		!validOpaqueID(sp.ProviderPaymentCharge) || upd.Message.From == nil || upd.Message.From.ID <= 0 {
		writeWebhookIgnored(w, "validation")
		return
	}
	ctx := r.Context()
	ownerTG := upd.Message.From.ID
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Ошибка")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	p, err := s.loadWebhookPayment(ctx, tx, sp.InvoicePayload)
	if errors.Is(err, pgx.ErrNoRows) {
		writeWebhookDuplicate(w)
		return
	}
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Ошибка")
		return
	}
	commitMismatch := func(reason string, reconcile bool) bool {
		if err := s.markWebhookMismatch(ctx, tx, p, *sp, ownerTG, reason, reconcile); err != nil {
			apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Ошибка")
			return false
		}
		if err := tx.Commit(ctx); err != nil {
			apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Ошибка")
			return false
		}
		return true
	}
	if p.Status == "succeeded" || p.Status == "refunded" {
		knownTelegramCharge := telegramCharge(p.storedPayment)
		chargeMismatch := (knownTelegramCharge != "" && knownTelegramCharge != sp.TelegramPaymentCharge) ||
			(p.TelegramCharge != nil && *p.TelegramCharge != sp.TelegramPaymentCharge) ||
			(p.ProviderCharge != nil && *p.ProviderCharge != sp.ProviderPaymentCharge)
		ownerMismatch := p.ownerTG != nil && *p.ownerTG != ownerTG
		amountMismatch := sp.TotalAmount != p.Stars
		if chargeMismatch || ownerMismatch || amountMismatch {
			reason := "duplicate_charge"
			if !chargeMismatch && ownerMismatch && amountMismatch {
				reason = "duplicate_owner_amount_mismatch"
			} else if !chargeMismatch && ownerMismatch {
				reason = "duplicate_owner_mismatch"
			} else if !chargeMismatch && amountMismatch {
				reason = "duplicate_amount_mismatch"
			}
			if !commitMismatch(reason, false) {
				return
			}
		}
		writeWebhookDuplicate(w)
		return
	}
	if p.Status == "refunding" || (p.Status == "reconciliation" && !webhookReconciliationCanProceed(p)) {
		reason := "refunding_payment"
		if p.Status == "reconciliation" {
			reason = "reconciliation_blocked"
		}
		if !commitMismatch(reason, false) {
			return
		}
		writeWebhookReconciliation(w)
		return
	}
	if p.Status != "pending" && p.Status != "expired" && p.Status != "reconciliation" {
		writeWebhookDuplicate(w)
		return
	}
	var originalOwnerEvent *webhookIdentity
	if p.ReconciliationReason != nil || p.ownerTG == nil {
		identity, found, err := s.originalOwnerUnverifiedEvent(ctx, tx, p.ID)
		if err != nil {
			apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Ошибка")
			return
		}
		if found {
			originalOwnerEvent = &identity
		}
	}
	if p.ReconciliationReason != nil && *p.ReconciliationReason == "owner_unverified" && originalOwnerEvent == nil {
		if !commitMismatch("reconciliation_blocked", false) {
			return
		}
		writeWebhookReconciliation(w)
		return
	}
	if originalOwnerEvent != nil {
		ownerRecoveryReused := p.ReconciliationReason != nil && *p.ReconciliationReason == "charge_reused"
		if p.ownerTG == nil {
			writeWebhookReconciliation(w)
			return
		}
		if !webhookIdentityMatches(*originalOwnerEvent, *sp, ownerTG) {
			reused, err := s.webhookChargeReused(ctx, tx, p.ID, originalOwnerEvent.telegramCharge, originalOwnerEvent.providerCharge)
			if err != nil {
				apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Ошибка")
				return
			}
			if !reused {
				reused, err = s.webhookChargeReused(ctx, tx, p.ID, sp.TelegramPaymentCharge, sp.ProviderPaymentCharge)
				if err != nil {
					apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Ошибка")
					return
				}
			}
			reason := "charge_mismatch"
			if ownerTG != originalOwnerEvent.ownerTG {
				reason = "owner_mismatch"
			}
			if reused || ownerRecoveryReused {
				reason = "charge_reused"
			}
			if !commitMismatch(reason, true) {
				return
			}
			writeWebhookReconciliation(w)
			return
		}
		reused, err := s.webhookChargeReused(ctx, tx, p.ID, sp.TelegramPaymentCharge, sp.ProviderPaymentCharge)
		if err != nil {
			apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Ошибка")
			return
		}
		if reused || ownerRecoveryReused {
			if !commitMismatch("charge_reused", true) {
				return
			}
			writeWebhookReconciliation(w)
			return
		}
	}
	if p.ownerTG == nil {
		knownTelegramCharge := telegramCharge(p.storedPayment)
		if (knownTelegramCharge != "" && knownTelegramCharge != sp.TelegramPaymentCharge) ||
			(p.TelegramCharge != nil && *p.TelegramCharge != sp.TelegramPaymentCharge) ||
			(p.ProviderCharge != nil && *p.ProviderCharge != sp.ProviderPaymentCharge) {
			if !commitMismatch("charge_mismatch", true) {
				return
			}
		} else {
			reused, err := s.webhookChargeReused(ctx, tx, p.ID, sp.TelegramPaymentCharge, sp.ProviderPaymentCharge)
			if err != nil {
				apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Ошибка")
				return
			}
			stateReason := "owner_unverified"
			if reused {
				stateReason = "charge_reused"
			}
			if err := s.markOwnerUnverifiedReconciliation(ctx, tx, p, *sp, ownerTG, stateReason, !reused); err != nil {
				apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Ошибка")
				return
			}
			if err := tx.Commit(ctx); err != nil {
				apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Ошибка")
				return
			}
		}
		writeWebhookReconciliation(w)
		return
	}
	if (p.TelegramCharge != nil && *p.TelegramCharge != sp.TelegramPaymentCharge) ||
		(p.ProviderCharge != nil && *p.ProviderCharge != sp.ProviderPaymentCharge) {
		if !commitMismatch("charge_mismatch", true) {
			return
		}
		writeWebhookReconciliation(w)
		return
	}
	if ownerTG != *p.ownerTG {
		if !commitMismatch("owner_mismatch", true) {
			return
		}
		writeWebhookReconciliation(w)
		return
	}
	if sp.TotalAmount != p.Stars {
		if !commitMismatch("amount_mismatch", true) {
			return
		}
		writeWebhookReconciliation(w)
		return
	}
	reused, err := s.webhookChargeReused(ctx, tx, p.ID, sp.TelegramPaymentCharge, sp.ProviderPaymentCharge)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Ошибка")
		return
	}
	if reused {
		if !commitMismatch("charge_reused", true) {
			return
		}
		writeWebhookReconciliation(w)
		return
	}
	late := p.Status == "expired" || p.Status == "reconciliation"
	note := ""
	if late {
		note = "late-payment"
	}
	var updated bool
	err = tx.QueryRow(ctx, `UPDATE payments
		SET status='succeeded', provider_payment_id='tg:'||$2,
			telegram_payment_charge_id=$2, provider_payment_charge_id=$3,
			stars=$4, provider_verified_at=now(), paid_at=COALESCE(paid_at, now()),
			reconciliation_reason=NULL,
			note=CASE WHEN NULLIF(btrim(note), '') IS NULL THEN NULLIF($5,'')
				WHEN $5='' THEN note ELSE note || ' ' || $5 END
		WHERE id=$1 AND status IN ('pending','expired','reconciliation')
		  AND (reconciliation_reason IS NULL OR reconciliation_reason IN ('owner_unverified','amount_mismatch','charge_mismatch','owner_mismatch','charge_reused'))
		  AND (telegram_payment_charge_id IS NULL OR telegram_payment_charge_id=$2)
		  AND (provider_payment_charge_id IS NULL OR provider_payment_charge_id=$3)
		RETURNING true`, p.ID, sp.TelegramPaymentCharge, sp.ProviderPaymentCharge, sp.TotalAmount, note).Scan(&updated)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Ошибка")
		return
	}
	if !updated {
		writeWebhookDuplicate(w)
		return
	}
	if err := s.grantPaymentEntitlements(ctx, tx, p.storedPayment); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Ошибка начисления")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Ошибка")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if late {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "late": true})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (s *Service) HandleVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PaymentID         string `json:"payment_id"`
		ProviderPaymentID string `json:"provider_payment_id"`
	}
	if !apierr.Decode(w, r, &req) {
		return
	}
	uid := auth.UserID(r.Context())
	var status string
	var err error
	if req.PaymentID != "" {
		err = s.pg.QueryRow(r.Context(),
			`SELECT status FROM payments WHERE id=$1 AND user_id=$2`, req.PaymentID, uid).Scan(&status)
	} else if req.ProviderPaymentID != "" {
		err = s.pg.QueryRow(r.Context(),
			`SELECT status FROM payments WHERE user_id=$2
			 AND (provider_payment_id=$1 OR provider_payment_charge_id=$1)`, req.ProviderPaymentID, uid).Scan(&status)
	} else {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нужен payment_id")
		return
	}
	if err != nil {
		apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Платеж не найден")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": status})
}

func (s *Service) HandleAdminList(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 200 {
		limit = n
	}
	rows, err := s.pg.Query(r.Context(), `
		SELECT id, user_id, plan_code, price_rub_snapshot, provider, status, amount_rub, stars, note, created_at,
		       COALESCE(refund_state, 'none'), reconciliation_reason
		FROM payments ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось загрузить")
		return
	}
	defer rows.Close()
	type row struct {
		ID, UserID, Plan, Provider, Status, Note, Created string
		Price, Amount, Stars                              int
		RefundState, ReconciliationReason                 string
	}
	out := []row{}
	for rows.Next() {
		var x row
		var note *string
		var reason *string
		var ts time.Time
		if err := rows.Scan(&x.ID, &x.UserID, &x.Plan, &x.Price, &x.Provider, &x.Status, &x.Amount, &x.Stars, &note, &ts, &x.RefundState, &reason); err != nil {
			continue
		}
		if note != nil {
			x.Note = *note
		}
		if reason != nil {
			x.ReconciliationReason = *reason
		}
		x.Created = ts.Format(time.RFC3339)
		out = append(out, x)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Service) ExpirePending(ctx context.Context) (int64, error) {
	res, err := s.pg.Exec(ctx,
		`UPDATE payments SET status='expired' WHERE status='pending' AND created_at < now() - interval '15 minutes'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected(), nil
}

func splitCharge(s string) []string {
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return []string{s[:i], s[i+1:]}
	}
	return []string{s}
}

func telegramCharge(p storedPayment) string {
	if p.TelegramCharge != nil {
		if !validOpaqueID(*p.TelegramCharge) {
			return ""
		}
		return *p.TelegramCharge
	}
	parts := splitCharge(p.ProviderPaymentID)
	if len(parts) == 2 && parts[0] == "tg" && validOpaqueID(parts[1]) {
		return parts[1]
	}
	return ""
}

func (s *Service) startRefund(ctx context.Context, id string) (refundPayment, bool, error) {
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return refundPayment{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	p, err := s.loadRefundPayment(ctx, tx, id, true)
	if err != nil {
		return refundPayment{}, false, err
	}
	if p.Status != "succeeded" {
		if err := tx.Commit(ctx); err != nil {
			return refundPayment{}, false, err
		}
		return p, false, nil
	}
	charge := telegramCharge(p.storedPayment)
	tag, err := tx.Exec(ctx, `UPDATE payments
		SET status='refunding', refund_state='requested', refund_requested_at=now(),
			refund_attempted_at=NULL, refund_confirmed_at=NULL, refund_last_error=NULL,
			reconciliation_reason=NULL
		WHERE id=$1 AND status='succeeded'`, id)
	if err != nil {
		return refundPayment{}, false, err
	}
	if tag.RowsAffected() != 1 {
		if err := tx.Commit(ctx); err != nil {
			return refundPayment{}, false, err
		}
		return p, false, nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO payment_refunds
		(payment_id, state, charge_id, requested_at, updated_at)
		VALUES ($1,'requested',NULLIF($2,''),now(),now())
		ON CONFLICT (payment_id) DO UPDATE SET state='requested', charge_id=EXCLUDED.charge_id,
			requested_at=now(), submitted_at=NULL, confirmed_at=NULL, last_error=NULL, updated_at=now()`,
		id, charge)
	if err != nil {
		return refundPayment{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return refundPayment{}, false, err
	}
	p.Status = "refunding"
	p.RefundState = "requested"
	p.refundTableState = "requested"
	return p, true, nil
}

func (s *Service) updateRefundState(ctx context.Context, id, state, lastError string) error {
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE payment_refunds
		SET state=$2, last_error=NULLIF($3,''), updated_at=now(),
			confirmed_at=CASE WHEN $2='confirmed' THEN COALESCE(confirmed_at,now()) ELSE confirmed_at END
		WHERE payment_id=$1`, id, state, lastError)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("refund attempt not found")
	}
	tag, err = tx.Exec(ctx, `UPDATE payments
		SET refund_state=$2, refund_last_error=NULLIF($3,''),
			reconciliation_reason=CASE WHEN $2='unknown' THEN 'refund_unknown' ELSE NULL END,
			refund_confirmed_at=CASE WHEN $2='confirmed' THEN COALESCE(refund_confirmed_at,now()) ELSE refund_confirmed_at END
		WHERE id=$1 AND status='refunding'`, id, state, lastError)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("refund payment state changed")
	}
	return tx.Commit(ctx)
}

func (s *Service) markRefundSubmitted(ctx context.Context, id string) error {
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE payment_refunds
		SET state='submitted', submitted_at=now(), updated_at=now()
		WHERE payment_id=$1 AND state='requested'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("refund attempt is not requestable")
	}
	tag, err = tx.Exec(ctx, `UPDATE payments
		SET refund_state='submitted', refund_attempted_at=now()
		WHERE id=$1 AND status='refunding' AND refund_state='requested'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("refund payment state changed")
	}
	return tx.Commit(ctx)
}

func (s *Service) finalizeRefund(ctx context.Context, id string) error {
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var state string
	var note *string
	var duration *int
	err = tx.QueryRow(ctx, `SELECT refund_state, note, duration_days_snapshot FROM payments WHERE id=$1 FOR UPDATE`, id).Scan(&state, &note, &duration)
	if err != nil {
		return err
	}
	if state != "confirmed" && state != "manual" {
		return fmt.Errorf("refund is not ready")
	}
	suffix := ""
	if state == "manual" {
		suffix = "manual-no-tg-data"
	}
	tag, err := tx.Exec(ctx, `UPDATE payments
		SET status='refunded',
			note=CASE WHEN NULLIF(btrim(note), '') IS NULL THEN NULLIF($2,'')
				WHEN $2='' THEN note ELSE note || ' ' || $2 END
		WHERE id=$1 AND status='refunding' AND refund_state IN ('confirmed','manual')`, id, suffix)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("refund state changed")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM single_entitlements WHERE payment_id=$1`, id); err != nil {
		return err
	}
	days := 30
	if duration != nil && *duration > 0 {
		days = *duration
	}
	if _, err := tx.Exec(ctx, `UPDATE subscriptions
		SET valid_until=GREATEST(valid_until - make_interval(days => $2), now()),
			status=CASE WHEN valid_until - make_interval(days => $2) <= now() THEN 'revoked' ELSE status END
		WHERE payment_id=$1 AND status='active'`, id, days); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_refunds SET updated_at=now() WHERE payment_id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) respondExistingRefund(w http.ResponseWriter, r *http.Request, p refundPayment) {
	switch p.Status {
	case "refunded":
		apierr.Write(w, http.StatusConflict, "ALREADY_REFUNDED", "Возврат уже оформлен")
	case "refunding":
		state := p.refundTableState
		if state == "" {
			state = p.RefundState
		}
		if state == "confirmed" || state == "manual" {
			if err := s.finalizeRefund(r.Context(), p.ID); err != nil {
				apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "reconciled": true})
			return
		}
		apierr.Write(w, http.StatusConflict, "REFUND_RECONCILIATION_REQUIRED", "Исход возврата не подтвержден; нужен ручной reconcile")
	case "reconciliation":
		apierr.Write(w, http.StatusConflict, "REFUND_RECONCILIATION_REQUIRED", "Платёж ожидает reconcile")
	default:
		apierr.Write(w, http.StatusConflict, "REFUND_NOT_SUCCEEDED", "Возврат доступен только после успешной оплаты")
	}
}

func (s *Service) HandleRefund(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PaymentID string `json:"payment_id"`
	}
	if !apierr.Decode(w, r, &req) || req.PaymentID == "" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нужен payment_id")
		return
	}
	ctx := r.Context()
	current, err := s.loadRefundPayment(ctx, s.pg, req.PaymentID, false)
	if errors.Is(err, pgx.ErrNoRows) {
		apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Платеж не найден")
		return
	}
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Платеж не найден")
		return
	}
	charge := telegramCharge(current.storedPayment)
	hasOwner := current.ownerTG != nil && *current.ownerTG > 0
	if current.Status == "succeeded" && charge != "" && hasOwner && !tgTokenReady() {
		apierr.Write(w, http.StatusBadGateway, "TG_REFUND_FAILED", "Telegram не настроен, подписка не тронута")
		return
	}
	p, transitioned, err := s.startRefund(ctx, req.PaymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Платеж не найден")
		return
	}
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
		return
	}
	if !transitioned {
		s.respondExistingRefund(w, r, p)
		return
	}
	charge = telegramCharge(p.storedPayment)
	chargeKnown := p.TelegramCharge != nil || strings.HasPrefix(p.ProviderPaymentID, "tg:")
	if chargeKnown && charge == "" {
		_ = s.updateRefundState(ctx, p.ID, "unknown", "invalid_telegram_charge")
		apierr.Write(w, http.StatusConflict, "REFUND_RECONCILIATION_REQUIRED", "Данные возврата требуют reconcile")
		return
	}
	if charge == "" {
		if err := s.updateRefundState(ctx, p.ID, "manual", ""); err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
			return
		}
		if err := s.finalizeRefund(ctx, p.ID); err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		return
	}
	if p.ownerTG == nil || *p.ownerTG <= 0 {
		_ = s.updateRefundState(ctx, p.ID, "unknown", "missing_telegram_owner")
		apierr.Write(w, http.StatusConflict, "REFUND_RECONCILIATION_REQUIRED", "Владелец Telegram недоступен, нужен reconcile")
		return
	}
	if err := s.markRefundSubmitted(ctx, p.ID); err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
		return
	}
	if _, err := s.tgCall(ctx, "refundStarPayment", map[string]any{
		"user_id": *p.ownerTG, "telegram_payment_charge_id": charge,
	}); err != nil {
		_ = s.updateRefundState(ctx, p.ID, "unknown", "telegram_refund_unknown")
		apierr.Write(w, http.StatusBadGateway, "REFUND_RECONCILIATION_REQUIRED", "Исход возврата не подтвержден, нужен reconcile")
		return
	}
	if err := s.updateRefundState(ctx, p.ID, "confirmed", ""); err != nil {
		apierr.Write(w, http.StatusBadGateway, "REFUND_RECONCILIATION_REQUIRED", "Возврат подтверждён провайдером, но finalize не завершён")
		return
	}
	if err := s.finalizeRefund(ctx, p.ID); err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
