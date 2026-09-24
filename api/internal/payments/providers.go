// Провайдеры оплаты: интерфейс + Stars + ЮKassa-скелет (см. V13, V15, 04-architecture/07).
// ЮKassa за флагом payments.yookassa {enabled}: без KYC — 501 с объяснением.
package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Provider — контракт платежного провайдера (см. V13).
type Provider interface {
	// Name — код провайдера (tg_stars, yookassa, boosty).
	Name() string
	// CreateInvoice создает счет, возвращает ссылку на оплату.
	CreateInvoice(ctx context.Context, http *http.Client, paymentID, planCode string, stars, amountRub int) (string, error)
}

// StarsProvider — Telegram Stars через Bot API (см. T29).
type StarsProvider struct{}

func (StarsProvider) Name() string { return "tg_stars" }

func (StarsProvider) CreateInvoice(ctx context.Context, client *http.Client, paymentID, planCode string, stars, amountRub int) (string, error) {
	token := os.Getenv("TG_BOT_TOKEN")
	if token == "" || token == "dev-only-bot" {
		return "", fmt.Errorf("no bot token")
	}
	body, _ := json.Marshal(map[string]any{
		"title":       "Онлайн Таро — " + planCode,
		"description": "Безлимит толкований и все расклады",
		"payload":     paymentID,
		"currency":    "XTR",
		"prices":      []map[string]any{{"label": planCode, "amount": stars}},
	})
	req, err := http.NewRequestWithContext(ctx,
		"POST", "https://api.telegram.org/bot"+token+"/createInvoiceLink", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Desc   string          `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if !out.OK {
		return "", fmt.Errorf("tg api: %s", out.Desc)
	}
	var link string
	_ = json.Unmarshal(out.Result, &link)
	return link, nil
}

// YooKassaProvider — скелет за флагом (см. V15, 04-architecture/07 №3).
// Без ИП/самозанятости + KYC — 501. amount_rub уже дублируется в payments (см. T29).
type YooKassaProvider struct{ pg *pgxpool.Pool }

func (YooKassaProvider) Name() string { return "yookassa" }

func (p YooKassaProvider) enabled(ctx context.Context) bool {
	var raw json.RawMessage
	if err := p.pg.QueryRow(ctx, `SELECT value FROM app_config WHERE key='payments.yookassa'`).Scan(&raw); err != nil {
		return false
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	on, _ := m["enabled"].(bool)
	return on
}

func (YooKassaProvider) CreateInvoice(ctx context.Context, client *http.Client, paymentID, planCode string, stars, amountRub int) (string, error) {
	return "", fmt.Errorf("yookassa disabled: нужен ИП/самозанятость + KYC (см. V15)")
}
