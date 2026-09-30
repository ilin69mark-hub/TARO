package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"taro/api/internal/payments"
	"taro/api/internal/store"
)

// paymentsReconcile — читатель payment_webhook_events (A09/F-09). До него
// таблицу писали, но не читал никто: дубликаты списаний и расхождения вебхуков
// оставались незамеченными.
//
// Вывод — текстом для человека и JSON для автоматизации. Код возврата 2, если
// есть записи, требующие внимания: на этом строится алерт (cron + проверка
// кода возврата), потому что метрик и алертов в проекте пока нет (F-20).
//
//	adminctl payments reconcile --since 1970-01-01 [--limit 200] [--json] [--all]
func paymentsReconcile(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("payments reconcile", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	sinceFlag := fs.String("since", "", "only events at or after this RFC3339 date (default: 24h ago)")
	limitFlag := fs.Int("limit", 200, "max grouped rows")
	allFlag := fs.Bool("all", false, "ignore --since and scan all history")
	asJSON := fs.Bool("json", false, "machine readable output")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument: %s\n", fs.Arg(0))
		return 1
	}
	since := time.Now().Add(-24 * time.Hour)
	if !*allFlag {
		if strings.TrimSpace(*sinceFlag) == "" {
			fmt.Fprintf(os.Stderr, "hint: --since not set, scanning the last 24h (use --all for full history)\n")
		} else {
			parsed, err := parseSince(strings.TrimSpace(*sinceFlag))
			if err != nil {
				fmt.Fprintf(os.Stderr, "invalid --since: %v\n", err)
				return 1
			}
			since = parsed
		}
	} else if strings.TrimSpace(*sinceFlag) != "" {
		fmt.Fprintf(os.Stderr, "--all and --since are mutually exclusive\n")
		return 1
	}

	pg, err := store.ConnectPG(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "database: %v\n", err)
		return 1
	}
	defer pg.Close()

	items, err := payments.ReconcileWebhookEvents(ctx, pg, since, *limitFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reconcile: %v\n", err)
		return 1
	}
	attention, byReason := payments.WebhookEventSummary(items)

	if *asJSON {
		if items == nil {
			items = []payments.WebhookMismatch{}
		}
		fmt.Printf("{\"since\":%q,\"rows\":%d,\"needs_attention\":%d,\"by_reason\":%s,\"items\":%s}\n",
			since.UTC().Format(time.RFC3339), len(items), attention, reasonJSON(byReason), itemsJSON(items))
	} else {
		printReconcileHuman(items, byReason, attention, since)
	}
	if attention > 0 {
		return 2
	}
	return 0
}

func printReconcileHuman(items []payments.WebhookMismatch, byReason map[string]int, attention int, since time.Time) {
	if len(items) == 0 {
		fmt.Printf("no webhook mismatches since %s\n", since.UTC().Format(time.RFC3339))
		return
	}
	reasons := make([]string, 0, len(byReason))
	for r := range byReason {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	parts := make([]string, 0, len(reasons))
	for _, r := range reasons {
		parts = append(parts, fmt.Sprintf("%s=%d", r, byReason[r]))
	}
	fmt.Printf("webhook mismatches since %s: %d payments, %d need attention (%s)\n",
		since.UTC().Format(time.RFC3339), len(items), attention, strings.Join(parts, " "))
	fmt.Printf(" %-37s %-9s %-18s %-13s %-11s %s\n", "PAYMENT", "USER", "REASON", "STATUS", "REFUND", "LAST SEEN")
	for _, m := range items {
		flagMark := " "
		if m.NeedsAttention() {
			flagMark = "!"
		}
		fmt.Printf("%s%-37s %-9s %-18s %-13s %-11s %s x%d %d₽\n",
			flagMark, m.PaymentID, shortID(m.UserID), m.Reason, m.PaymentStatus, m.RefundState,
			m.LastSeen.UTC().Format("2006-01-02 15:04"), m.Events, m.AmountRub)
	}
	if attention > 0 {
		fmt.Println("\n! строки с '!' требуют разбора: рефанд/ручная проверка платежа в Telegram")
	}
}

func shortID(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}

// parseSince принимает и полный RFC3339, и голую дату YYYY-MM-DD: оператор
// в cron пишет «с прошлой ночи», а не timestamp.
func parseSince(raw string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("want RFC3339 (2026-09-26T00:00:00Z) or YYYY-MM-DD, got %q", raw)
}

func reasonJSON(byReason map[string]int) string {
	keys := make([]string, 0, len(byReason))
	for k := range byReason {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%q:%d", k, byReason[k]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func itemsJSON(items []payments.WebhookMismatch) string {
	parts := make([]string, 0, len(items))
	for _, m := range items {
		parts = append(parts, fmt.Sprintf(
			`{"payment_id":%q,"user_id":%q,"reason":%q,"events":%d,"payment_status":%q,"refund_state":%q,"amount_rub":%d,"last_seen":%q,"needs_attention":%t}`,
			m.PaymentID, m.UserID, m.Reason, m.Events, m.PaymentStatus, m.RefundState,
			m.AmountRub, m.LastSeen.UTC().Format(time.RFC3339), m.NeedsAttention()))
	}
	return "[" + strings.Join(parts, ",") + "]"
}
