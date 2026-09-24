package ai

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type workerJob struct {
	id        string
	spread    string
	question  string
	cards     []CardValue
	cardsRaw  json.RawMessage
	positions []Position
	token     string
}

func (g *Gateway) StartWorker(ctx context.Context, pg *pgxpool.Pool, tick time.Duration) {
	if tick <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(tick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				func() {
					defer func() { _ = recover() }()
					g.drainOnce(ctx, pg)
				}()
			}
		}
	}()
}

func (g *Gateway) drainOnce(ctx context.Context, pg *pgxpool.Pool) {
	defer func() { _ = recover() }()
	if pg == nil {
		return
	}
	rows, err := pg.Query(ctx, `
		WITH candidates AS (
			SELECT id
			  FROM readings
			 WHERE status IN ('pending', 'pending_fallback')
			   AND (worker_lease_until IS NULL OR worker_lease_until <= now())
			 ORDER BY updated_at, created_at
			 FOR UPDATE SKIP LOCKED
			 LIMIT 5
		)
		UPDATE readings r
		   SET worker_claim_token=gen_random_uuid(),
		       worker_lease_until=now()+interval '2 minutes',
		       worker_attempts=worker_attempts+1,
		       updated_at=now()
		  FROM candidates
		 WHERE r.id=candidates.id
		RETURNING r.id::text, r.spread_code, COALESCE(r.question,''), r.cards, r.worker_claim_token::text`)
	if err != nil {
		return
	}
	jobs := make([]workerJob, 0, 5)
	for rows.Next() {
		var job workerJob
		if err := rows.Scan(&job.id, &job.spread, &job.question, &job.cardsRaw, &job.token); err == nil {
			jobs = append(jobs, job)
		}
	}
	rows.Close()
	for i := range jobs {
		job := &jobs[i]
		var draws []struct {
			CardID   int  `json:"card_id"`
			Reversed bool `json:"reversed"`
			Position int  `json:"position"`
		}
		if json.Unmarshal(job.cardsRaw, &draws) != nil {
			g.releaseClaim(pg, job.id, job.token, time.Minute)
			continue
		}
		for _, draw := range draws {
			var name, upright, reversed string
			if err := pg.QueryRow(ctx,
				`SELECT name_ru, upright_ru, reversed_ru FROM cards WHERE id=$1`, draw.CardID).Scan(&name, &upright, &reversed); err != nil {
				job.cards = nil
				break
			}
			job.cards = append(job.cards, CardValue{
				Name: name, Upright: upright, ReversedText: reversed,
				Reversed: draw.Reversed, Position: draw.Position, CardID: draw.CardID,
			})
		}
		var spreadName string
		var positionsRaw json.RawMessage
		if err := pg.QueryRow(ctx, `SELECT name_ru, positions FROM spreads WHERE code=$1`, job.spread).Scan(&spreadName, &positionsRaw); err != nil {
			job.spread = ""
		} else {
			_ = json.Unmarshal(positionsRaw, &job.positions)
			job.spread = spreadName
		}
		if len(job.cards) == 0 || job.spread == "" {
			g.releaseClaim(pg, job.id, job.token, time.Minute)
			continue
		}
		jobCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		text := g.completeReading(jobCtx, job.id, job.spread, job.positions, job.cards, job.question)
		cancel()
		if text == "" {
			g.releaseClaim(pg, job.id, job.token, 30*time.Second)
			continue
		}
		status := "done"
		if ContainsStopWords(text) || text == SafeReplacement {
			text = SafeReplacement
			status = "filtered"
		}
		persistCtx, persistCancel := persistenceContext()
		_, _ = pg.Exec(persistCtx, `
			UPDATE readings
			   SET interpretation=$1, status=$2, worker_claim_token=NULL,
			       worker_lease_until=NULL, updated_at=now()
			 WHERE id=$3 AND worker_claim_token=$4 AND status IN ('pending', 'pending_fallback')`,
			text, status, job.id, job.token)
		persistCancel()
	}
}

func (g *Gateway) releaseClaim(pg *pgxpool.Pool, id, token string, delay time.Duration) {
	if pg == nil || id == "" || token == "" {
		return
	}
	seconds := int(delay / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	ctx, cancel := persistenceContext()
	defer cancel()
	_, _ = pg.Exec(ctx, `
		UPDATE readings
		   SET worker_claim_token=NULL, worker_lease_until=now()+make_interval(secs => $3), updated_at=now()
		 WHERE id=$1 AND worker_claim_token=$2 AND status IN ('pending', 'pending_fallback')`,
		id, token, seconds)
}

func (g *Gateway) complete(ctx context.Context, spread string, positions []Position, cards []CardValue, question string) string {
	return g.completeReading(ctx, "", spread, positions, cards, question)
}

func (g *Gateway) completeReading(ctx context.Context, readingID, spread string, positions []Position, cards []CardValue, question string) (result string) {
	ch := make(chan string, MaxOutputBytes/512+16)
	defer func() {
		if recover() != nil {
			result = ""
		}
	}()
	text, _, _, _ := g.Stream(ctx, readingID, spread, positions, cards, question, ch)
	for range ch {
	}
	return text
}
