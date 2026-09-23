// Worker дописывания pending_fallback (см. 04-architecture/06-ai-pipeline.md, T12/T13).
// Тик 30с: берет до 5 зависших, копит стрим без клиента, пишет done (или оставляет pending).
package ai

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// StartWorker запускает горутину-воркер до отмены ctx.
func (g *Gateway) StartWorker(ctx context.Context, pg *pgxpool.Pool, tick time.Duration) {
	go func() {
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				g.drainOnce(ctx, pg)
			}
		}
	}()
}

// drainOnce — одна итерация: до 5 pending_fallback.
func (g *Gateway) drainOnce(ctx context.Context, pg *pgxpool.Pool) {
	rows, err := pg.Query(ctx, `
		SELECT id, spread_code, COALESCE(question,''), cards FROM readings
		 WHERE status='pending_fallback' ORDER BY created_at LIMIT 5`)
	if err != nil {
		return
	}
	defer rows.Close()
	type job struct {
		id, spread, question string
		cards                []CardValue
		positions            []Position
	}
	var jobs []job
	for rows.Next() {
		var j job
		var cardsRaw, posRaw json.RawMessage
		var spreadName string
		if err := rows.Scan(&j.id, &j.spread, &j.question, &cardsRaw); err != nil {
			continue
		}
		var draws []struct {
			CardID   int  `json:"card_id"`
			Reversed bool `json:"reversed"`
			Position int  `json:"position"`
		}
		if json.Unmarshal(cardsRaw, &draws) != nil {
			continue
		}
		for _, d := range draws {
			var name, up, rev string
			if err := pg.QueryRow(ctx,
				`SELECT name_ru, upright_ru, reversed_ru FROM cards WHERE id=$1`, d.CardID).Scan(&name, &up, &rev); err != nil {
				continue
			}
			j.cards = append(j.cards, CardValue{Name: name, Upright: up, ReversedText: rev, Reversed: d.Reversed, Position: d.Position, CardID: d.CardID})
		}
		if err := pg.QueryRow(ctx, `SELECT name_ru, positions FROM spreads WHERE code=$1`, j.spread).Scan(&spreadName, &posRaw); err != nil {
			continue
		}
		_ = json.Unmarshal(posRaw, &j.positions)
		j.spread = spreadName
		jobs = append(jobs, j)
	}
	rows.Close()
	for _, j := range jobs {
		text := g.complete(ctx, j.spread, j.positions, j.cards, j.question)
		if text == "" {
			continue // AI все еще лежит — оставляем pending_fallback
		}
		if ContainsStopWords(text) {
			_, _ = pg.Exec(ctx,
				`UPDATE readings SET interpretation=$1, status='filtered' WHERE id=$2 AND status='pending_fallback'`,
				SafeReplacement, j.id)
			g.log(ctx, j.id, "filter", "", 0, 0, 0, "filtered", "stop-words")
			continue
		}
		_, _ = pg.Exec(ctx,
			`UPDATE readings SET interpretation=$1, status='done' WHERE id=$2 AND status='pending_fallback'`, text, j.id)
	}
}

// complete копит стрим без клиента (worker). Пусто при недоступности AI.
// Дренаж concurrent — иначе дедлок на текстах длиннее буфера.
func (g *Gateway) complete(ctx context.Context, spread string, positions []Position, cards []CardValue, question string) string {
	ch := make(chan string, 256)
	type res struct {
		text string
		err  error
	}
	done := make(chan res, 1)
	go func() {
		text, _, _, err := g.Stream(ctx, "", spread, positions, cards, question, ch)
		done <- res{text, err}
	}()
	for range ch {
	}
	r := <-done
	if r.err != nil {
		return ""
	}
	return r.text
}
