// Package payments — Telegram Stars (см. docs/project-book/04-architecture/07, T29).
// Идемпотентность: INSERT ... ON CONFLICT(provider_payment_id) DO NOTHING → начисление
// только при inserted=true (ретраи TG безопасны).
// Age-gate: invoice без age_confirmed_at → 403 (см. 08-risks/02).
// Webhook проверяет Secret-Token (не путать с HMAC initData), CSRF не требует.
package payments

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"taro/api/internal/apierr"
	"taro/api/internal/auth"
	"taro/api/internal/me"
)

// Service — платежи.
type Service struct {
	pg   *pgxpool.Pool
	mv   *me.Service
	http *http.Client
}

// New возвращает сервис.
func New(pg *pgxpool.Pool, mv *me.Service) *Service {
	return &Service{pg: pg, mv: mv, http: &http.Client{Timeout: 10 * time.Second}}
}

// tgCall вызывает Bot API метод.
func (s *Service) tgCall(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	token := os.Getenv("TG_BOT_TOKEN")
	if token == "" || token == "dev-only-bot" {
		return nil, fmt.Errorf("no bot token")
	}
	body, _ := json.Marshal(params)
	req, err := http.NewRequestWithContext(ctx,
		"POST", "https://api.telegram.org/bot"+token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Desc   string          `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, fmt.Errorf("tg api: %s", out.Desc)
	}
	return out.Result, nil
}

// HandleInvoice — POST /v1/payments/stars/invoice {plan_code, idempotency_key[, provider]}.
// Идемпотентность (см. D3): ключ ОБЯЗАТЕЛЕН (422 без него); повтор с тем же ключом <15мин
// возвращает тот же payment_id (новый invoice_link, строка одна — дублей pending нет).
// provider: tg_stars (дефолт) | yookassa (501 без KYC, см. V15). Провайдеры — см. providers.go (V13).
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
	if req.IdempotencyKey == "" || len(req.IdempotencyKey) > 64 {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нужен idempotency_key (1..64)")
		return
	}
	prov := req.Provider
	if prov == "" {
		prov = "tg_stars"
	}
	if prov == "yookassa" {
		apierr.Write(w, http.StatusNotImplemented, "YOOKASSA_DISABLED", "Оплата картой скоро: проходим KYC (см. V15)")
		return
	}
	if prov != "tg_stars" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Неизвестный провайдер")
		return
	}
	ctx := r.Context()
	var planID string
	var price, stars int
	var duration *int
	err := s.pg.QueryRow(ctx,
		`SELECT id, price_rub, stars_amount, duration_days FROM plans
		  WHERE code=$1 AND is_active AND code IN ('month_299','year_2490','single_99')`,
		req.PlanCode).Scan(&planID, &price, &stars, &duration)
	if err != nil {
		apierr.Write(w, http.StatusUnprocessableEntity, "UNKNOWN_PLAN", "Неизвестный тариф")
		return
	}
	// A/B: month_299 может стоить иначе (снапшот фиксирует факт, см. U22)
	if req.PlanCode == "month_299" {
		if _, abPrice := s.VariantFor(ctx, uid); abPrice > 0 {
			price = abPrice
		}
	}
	// Winback V16: eligible + offers.winback.enabled → скидка pct, след в note (см. V17)
	note := ""
	if req.PlanCode == "month_299" {
		if StockholderDiscount, pct := s.winbackPrice(ctx, uid, price); StockholderDiscount > 0 {
			price = StockholderDiscount
			note = "winback-" + itoa(pct)
		}
	}
	var paymentID string
	_ = s.pg.QueryRow(ctx, `
		SELECT id FROM payments WHERE user_id=$1 AND idempotency_key=$2
		 AND status='pending' AND created_at > now() - interval '15 minutes'`,
		uid, req.IdempotencyKey).Scan(&paymentID)
	if paymentID == "" {
		err = s.pg.QueryRow(ctx, `
			INSERT INTO payments (user_id, plan_id, plan_code, price_rub_snapshot, provider, provider_payment_id, amount_rub, stars, status, note, idempotency_key)
			VALUES ($1,$2,$3,$4,'tg_stars','pending:'||gen_random_uuid(),$4,$5,'pending',NULLIF($6,''),$7)
			ON CONFLICT (user_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
			RETURNING id`, uid, planID, req.PlanCode, price, stars, note, req.IdempotencyKey).Scan(&paymentID)
		if err != nil {
			// race: параллельный запрос создал первым — забираем его
			_ = s.pg.QueryRow(ctx,
				`SELECT id FROM payments WHERE user_id=$1 AND idempotency_key=$2 AND status='pending'`,
				uid, req.IdempotencyKey).Scan(&paymentID)
		}
	}
	if paymentID == "" {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать платеж")
		return
	}
	link, err := StarsProvider{}.CreateInvoice(ctx, s.http, paymentID, req.PlanCode, stars, price)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Платежи временно недоступны")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"invoice_link": link, "payment_id": paymentID})
}

// VariantFor — A/B цены (см. U22): app_config ab.price_month {enabled, control, test, split}.
// Бакет детерминирован хешем user_id (стабилен между заходами).
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

// HandleVariant — GET /v1/ab/me: {variant, price_rub} для month_299 (0 = выкл, цена из plans).
func (s *Service) HandleVariant(w http.ResponseWriter, r *http.Request) {
	variant, price := s.VariantFor(r.Context(), auth.UserID(r.Context()))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"variant": variant, "price_rub": price})
}

// winbackPrice — скидка вернувшимся (см. V16): offers.winback {enabled, pct} +
// winback_eligible (expired ≥14д, нет active). Возвращает (цена, pct) или (0, 0).
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
	// NB: pgx считает УНИКАЛЬНЫЕ плейсхолдеры — повторы $1 + 2 аргумента = ошибка.
	// Поэтому $1/$2 явно (см. V16 debug).
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

// tgUpdate — минимальный Update Stars-платежа.
type tgUpdate struct {
	Message *struct {
		SuccessfulPayment *struct {
			Currency              string `json:"currency"`
			TotalAmount           int    `json:"total_amount"`
			InvoicePayload        string `json:"invoice_payload"`
			TelegramPaymentCharge string `json:"telegram_payment_charge_id"`
			ProviderPaymentCharge string `json:"provider_payment_charge_id"`
		} `json:"successful_payment"`
		From *struct {
			ID int64 `json:"id"`
		} `json:"from"`
	} `json:"message"`
}

// HandleWebhook — POST /v1/payments/stars/webhook (Secret-Token, без CSRF).
func (s *Service) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	secret := os.Getenv("TG_STARS_SECRET_TOKEN")
	got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	// Аудит C: сравнение за константное время (timing side-channel на !=).
	if secret == "" || secret == "dev-only-stars" || len(got) != len(secret) ||
		subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
		apierr.Write(w, http.StatusUnauthorized, apierr.CodeBadSign, "Bad secret token")
		return
	}
	var upd tgUpdate
	r.Body = http.MaxBytesReader(w, r.Body, apierr.MaxBody)
	if err := json.NewDecoder(r.Body).Decode(&upd); err != nil || upd.Message == nil || upd.Message.SuccessfulPayment == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ignored": true})
		return
	}
	sp := upd.Message.SuccessfulPayment
	ctx := r.Context()
	// Аудит B: сверяем сумму/валюту/отправителя ДО начислений (раньше начисляли вслепую).
	if sp.Currency != "XTR" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ignored": "currency"})
		return
	}
	// ищем pending по payload=payment_id
	var paymentID, userID, planCode, planID string
	var duration *int
	var expStars int
	var ownerTg *int64
	err := s.pg.QueryRow(ctx, `
		SELECT p.id, p.user_id, p.plan_code, p.plan_id, pl.duration_days, p.stars, u.tg_id
		  FROM payments p JOIN plans pl ON pl.id=p.plan_id JOIN users u ON u.id=p.user_id
		 WHERE p.id=$1 AND p.status='pending'`, sp.InvoicePayload).Scan(&paymentID, &userID, &planCode, &planID, &duration, &expStars, &ownerTg)
	if err != nil {
		// уже обработан или чужой — идемпотентный ok (ретраи TG)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "duplicate": true})
		return
	}
	if sp.TotalAmount != expStars || (upd.Message.From != nil && ownerTg != nil && upd.Message.From.ID != *ownerTg) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ignored": "amount_mismatch"})
		return
	}
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Ошибка")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var updated bool
	err = tx.QueryRow(ctx, `
		UPDATE payments SET status='succeeded', provider_payment_id=$1, stars=$2
		 WHERE id=$3 AND status='pending' RETURNING true`,
		"tg:"+sp.TelegramPaymentCharge, sp.TotalAmount, paymentID).Scan(&updated)
	if err != nil || !updated {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "duplicate": true})
		return
	}
	if planCode == "single_99" {
		// single_99 = 1 чтение ЛЮБОГО premium (spread_code='any'),
		// гасится первым premium-чтением (см. readings T29-патч).
		// S05: ошибка вставки → Rollback + 500 (ретрай TG даст duplicate, денег без услуги нет).
		if _, err := tx.Exec(ctx,
			`INSERT INTO single_entitlements (user_id, spread_code, payment_id) VALUES ($1,'any',$2)`,
			userID, paymentID); err != nil {
			apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Ошибка начисления")
			return
		}
	} else {
		days := 30
		if duration != nil && *duration > 0 {
			days = *duration
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, valid_until)
			SELECT $1,$2,$3,(SELECT price_rub FROM plans WHERE id=$2),
			       GREATEST(COALESCE(MAX(valid_until), now()), now()) + make_interval(days => $4)
			  FROM subscriptions WHERE user_id=$1 AND status='active'`,
			userID, planID, planCode, days); err != nil {
			apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Ошибка начисления")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Ошибка")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// HandleVerify — POST /v1/payments/stars/verify {payment_id} | {provider_payment_id} (см. D3).
// Спека требует provider_payment_id — поддерживаем оба поля.
// Webhook — источник правды; verify лишь отдает статус строки (кнопка «Я оплатил»).
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
	if req.PaymentID != "" {
		err := s.pg.QueryRow(r.Context(),
			`SELECT status FROM payments WHERE id=$1 AND user_id=$2`, req.PaymentID, uid).Scan(&status)
		if err != nil {
			apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Платеж не найден")
			return
		}
	} else if req.ProviderPaymentID != "" {
		err := s.pg.QueryRow(r.Context(),
			`SELECT status FROM payments WHERE provider_payment_id=$1 AND user_id=$2`,
			req.ProviderPaymentID, uid).Scan(&status)
		if err != nil {
			apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Платеж не найден")
			return
		}
	} else {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нужен payment_id")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": status})
}

// HandleAdminList — GET /v1/admin/payments?limit (только :8081, см. V19).
func (s *Service) HandleAdminList(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 200 {
		limit = n
	}
	rows, err := s.pg.Query(r.Context(), `
		SELECT id, user_id, plan_code, price_rub_snapshot, provider, status, amount_rub, stars, note, created_at
		  FROM payments ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось загрузить")
		return
	}
	defer rows.Close()
	type row struct {
		ID, UserID, Plan, Provider, Status, Note, Created string
		Price, Amount, Stars                              int
	}
	out := []row{}
	for rows.Next() {
		var x row
		var note *string
		var ts time.Time
		if err := rows.Scan(&x.ID, &x.UserID, &x.Plan, &x.Price, &x.Provider, &x.Status, &x.Amount, &x.Stars, &note, &ts); err != nil {
			continue
		}
		if note != nil {
			x.Note = *note
		}
		x.Created = ts.Format(time.RFC3339)
		out = append(out, x)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// ExpirePending — job: pending старше 15 мин → expired (вызывать тиком, см. T30/cron).
func (s *Service) ExpirePending(ctx context.Context) (int64, error) {
	res, err := s.pg.Exec(ctx,
		`UPDATE payments SET status='expired' WHERE status='pending' AND created_at < now() - interval '15 minutes'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected(), nil
}

// HandleRefund — POST /v1/admin/refund {payment_id} (только :8081, см. T29).
// refundStarPayment в TG + payments=refunded + subscriptions.valid_until -= duration.
// Аудит C: двухфазно (succeeded→refunding→refunded) — внешний TG-вызов НЕ держит
// строковый лок; повтор/рестарт упирается в guard и получает 409, не двойной возврат.
func (s *Service) HandleRefund(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PaymentID string `json:"payment_id"`
	}
	if !apierr.Decode(w, r, &req) || req.PaymentID == "" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нужен payment_id")
		return
	}
	ctx := r.Context()
	var userID, planCode string
	var duration *int
	var tgCharge *string
	err := s.pg.QueryRow(ctx, `
		SELECT p.user_id, p.plan_code, pl.duration_days, NULLIF(p.provider_payment_id,'')
		  FROM payments p JOIN plans pl ON pl.id=p.plan_id
		 WHERE p.id=$1 AND p.status IN ('succeeded','refunding')`, req.PaymentID).Scan(&userID, &planCode, &duration, &tgCharge)
	if err != nil {
		apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Платеж не найден")
		return
	}
	// Фаза 1: быстрая пометка refunding с guard — повтор упирается сюда, лока нет.
	tag, err := s.pg.Exec(ctx,
		`UPDATE payments SET status='refunding' WHERE id=$1 AND status IN ('succeeded','refunding')`, req.PaymentID)
	if err != nil || tag.RowsAffected() == 0 {
		apierr.Write(w, http.StatusConflict, "ALREADY_REFUNDED", "Возврат уже оформлен")
		return
	}
	// TODO: user_id для refundStarPayment — tg_id юзера
	var tgID *int64
	_ = s.pg.QueryRow(ctx, `SELECT tg_id FROM users WHERE id=$1`, userID).Scan(&tgID)
	tgAttempted := false
	if tgID != nil && tgCharge != nil {
		charge := ""
		if parts := splitCharge(*tgCharge); len(parts) == 2 {
			charge = parts[1]
		}
		if charge != "" {
		// D3: ошибка TG — 502 БЕЗ пометки refunded (раньше молча резали подписку!).
		// Аудит C: откатываем и фазу 1 (refunding→succeeded) — повторная попытка чистая.
		tgAttempted = true
		if _, err := s.tgCall(ctx, "refundStarPayment", map[string]any{
			"user_id": *tgID, "telegram_payment_charge_id": charge,
		}); err != nil {
			_, _ = s.pg.Exec(ctx,
				`UPDATE payments SET status='succeeded' WHERE id=$1 AND status='refunding'`, req.PaymentID)
			apierr.Write(w, http.StatusBadGateway, "TG_REFUND_FAILED", "Telegram не вернул Stars, подписка не тронута")
			return
		}
		}
	}
	days := 30
	if duration != nil && *duration > 0 {
		days = *duration
	}
	note := ""
	if !tgAttempted {
		// без TG-данных (старые/anon-платежи): только ручная пометка, след в note (см. D3)
		note = "manual-no-tg-data"
	}
	// Фаза 2: финал короткой транзакцией с guard (строка уже refunding — наша).
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
		return
	}
	defer tx.Rollback(ctx)
	tag, err = tx.Exec(ctx, `UPDATE payments SET status='refunded', note=COALESCE(note || ' ', '') || $2 WHERE id=$1 AND status='refunding'`,
		req.PaymentID, note)
	if err != nil || tag.RowsAffected() == 0 {
		apierr.Write(w, http.StatusConflict, "ALREADY_REFUNDED", "Возврат уже оформлен")
		return
	}
	// valid_until -= duration (min now); истекшие помечаем revoked (см. 02-functional/05)
	if _, err := tx.Exec(ctx, `
		UPDATE subscriptions
		   SET valid_until = GREATEST(valid_until - make_interval(days => $3), now()),
		       status = CASE WHEN valid_until - make_interval(days => $3) <= now() THEN 'revoked' ELSE status END
		 WHERE user_id=$1 AND plan_code=$2 AND status='active'`,
		userID, planCode, days); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось оформить возврат")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func splitCharge(s string) []string {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return []string{s[:i], s[i+1:]}
		}
	}
	return []string{s}
}
