package ai

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"taro/api/internal/entitlements"
)

const (
	defaultWorkerMaxAttempts = 5
	defaultWorkerBackoff     = 30 * time.Second
	defaultWorkerMaxBackoff  = 15 * time.Minute
	workerBatchSize          = 1
	workerProcessingTimeout  = 30 * time.Second
	workerLeaseDuration      = 2 * time.Minute
)

type workerJob struct {
	id        string
	spread    string
	question  string
	cards     []CardValue
	cardsRaw  json.RawMessage
	positions []Position
	token     string
	attempts  int
}

type workerPolicy struct {
	maxAttempts int
	baseBackoff time.Duration
	maxBackoff  time.Duration
}

func envInt(name string, fallback, min, max int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value < min {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}

func loadWorkerPolicy() workerPolicy {
	policy := workerPolicy{
		maxAttempts: envInt("AI_WORKER_MAX_ATTEMPTS", defaultWorkerMaxAttempts, 1, 20),
		baseBackoff: time.Duration(envInt("AI_WORKER_BACKOFF_SECONDS", int(defaultWorkerBackoff/time.Second), 1, 3600)) * time.Second,
		maxBackoff:  time.Duration(envInt("AI_WORKER_MAX_BACKOFF_SECONDS", int(defaultWorkerMaxBackoff/time.Second), 1, 86400)) * time.Second,
	}
	if policy.maxBackoff < policy.baseBackoff {
		policy.maxBackoff = policy.baseBackoff
	}
	return policy
}

func (p workerPolicy) backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := p.baseBackoff
	for i := 1; i < attempt; i++ {
		if delay >= p.maxBackoff/2 {
			return p.maxBackoff
		}
		delay *= 2
	}
	if delay > p.maxBackoff {
		return p.maxBackoff
	}
	return delay
}

func workerJobContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, workerProcessingTimeout)
}

func (g *Gateway) StartWorker(ctx context.Context, pg *pgxpool.Pool, tick time.Duration) {
	g.startWorker(ctx, pg, tick)
}

func (g *Gateway) startWorker(ctx context.Context, pg *pgxpool.Pool, tick time.Duration) <-chan struct{} {
	done := make(chan struct{})
	if tick <= 0 {
		close(done)
		return done
	}
	go func() {
		defer close(done)
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
	return done
}

func (g *Gateway) drainOnce(ctx context.Context, pg *pgxpool.Pool) {
	defer func() { _ = recover() }()
	if pg == nil {
		return
	}
	policy := loadWorkerPolicy()
	recovery := entitlements.New(pg, g.rd)
	_ = recovery.RecoverPendingAuthorizations(ctx, 20)
	_, _ = pg.Exec(ctx, `
		UPDATE readings
		   SET status='failed', worker_claim_token=NULL, worker_lease_until=NULL, updated_at=now()
		 WHERE status IN ('pending', 'pending_fallback')
		   AND quota_state='allowed'
		   AND worker_attempts >= $1
		   AND (worker_lease_until IS NULL OR worker_lease_until <= now())`, policy.maxAttempts)
	rows, err := pg.Query(ctx, `
		WITH candidates AS (
			SELECT id
			  FROM readings
			 WHERE status IN ('pending', 'pending_fallback')
			   AND quota_state='allowed'
			   AND worker_attempts < $1
			   AND (worker_lease_until IS NULL OR worker_lease_until <= now())
			 ORDER BY updated_at, created_at
			 FOR UPDATE SKIP LOCKED
			 LIMIT $2
		)
		UPDATE readings r
		   SET worker_claim_token=gen_random_uuid(),
		       worker_lease_until=now()+make_interval(secs => $3),
		       worker_attempts=worker_attempts+1,
		       updated_at=now()
		  FROM candidates
		 WHERE r.id=candidates.id
		RETURNING r.id::text, r.spread_code, COALESCE(r.question,''), r.cards, r.worker_claim_token::text, r.worker_attempts`, policy.maxAttempts, workerBatchSize, int(workerLeaseDuration/time.Second))
	if err != nil {
		return
	}
	jobs := make([]workerJob, 0, workerBatchSize)
	for rows.Next() {
		var job workerJob
		if err := rows.Scan(&job.id, &job.spread, &job.question, &job.cardsRaw, &job.token, &job.attempts); err == nil {
			jobs = append(jobs, job)
		}
	}
	rows.Close()
	for i := range jobs {
		job := &jobs[i]
		jobCtx, cancel := workerJobContext(ctx)
		cards, ok := loadWorkerCards(jobCtx, pg, job.cardsRaw)
		if !ok {
			cancel()
			g.failClaim(pg, job, policy)
			continue
		}
		job.cards = cards
		positions, spread, complete := loadWorkerSpread(jobCtx, pg, job.cardsRaw, job.spread)
		if !complete || !g.Enabled() {
			cancel()
			g.persistTerminalFallback(pg, job, FallbackInterpretation(cards))
			continue
		}
		job.positions = positions
		job.spread = spread
		text := g.completeReading(jobCtx, job.id, job.spread, job.positions, job.cards, job.question)
		cancel()
		if text == "" {
			g.failClaim(pg, job, policy)
			continue
		}
		status := "done"
		if ContainsStopWords(text) || text == SafeReplacement {
			text = SafeReplacement
			status = "filtered"
		}
		persistCtx, persistCancel := persistenceContext()
		tag, err := pg.Exec(persistCtx, `
			UPDATE readings
			   SET interpretation=$1, status=$2, worker_claim_token=NULL,
			       worker_lease_until=NULL, updated_at=now()
			 WHERE id=$3 AND worker_claim_token=$4
			   AND status IN ('pending', 'pending_fallback') AND quota_state='allowed'`, text, status, job.id, job.token)
		persistCancel()
		if err != nil || tag.RowsAffected() != 1 {
			g.failClaim(pg, job, policy)
		}
	}
}

func loadWorkerCards(ctx context.Context, pg *pgxpool.Pool, raw json.RawMessage) ([]CardValue, bool) {
	var draws []struct {
		CardID   int  `json:"card_id"`
		Reversed bool `json:"reversed"`
		Position int  `json:"position"`
	}
	if pg == nil || json.Unmarshal(raw, &draws) != nil || len(draws) == 0 {
		return nil, false
	}
	values := make([]CardValue, 0, len(draws))
	seen := make(map[int]struct{}, len(draws))
	for i, draw := range draws {
		if draw.CardID < 0 || draw.CardID > 77 || draw.Position != i {
			return nil, false
		}
		if _, ok := seen[draw.CardID]; ok {
			return nil, false
		}
		seen[draw.CardID] = struct{}{}
		var name, upright, reversed string
		if err := pg.QueryRow(ctx,
			`SELECT name_ru, upright_ru, reversed_ru FROM cards WHERE id=$1`, draw.CardID).Scan(&name, &upright, &reversed); err != nil {
			return nil, false
		}
		if strings.TrimSpace(name) == "" || strings.TrimSpace(upright) == "" || strings.TrimSpace(reversed) == "" {
			return nil, false
		}
		values = append(values, CardValue{Name: name, Upright: upright, ReversedText: reversed, Reversed: draw.Reversed, Position: draw.Position, CardID: draw.CardID})
	}
	return values, true
}

func loadWorkerSpread(ctx context.Context, pg *pgxpool.Pool, raw json.RawMessage, spreadCode string) ([]Position, string, bool) {
	var draws []struct {
		Position int `json:"position"`
	}
	if pg == nil || json.Unmarshal(raw, &draws) != nil || len(draws) == 0 {
		return nil, "", false
	}
	var spreadName string
	var positionsRaw json.RawMessage
	if err := pg.QueryRow(ctx, `SELECT name_ru, positions FROM spreads WHERE code=$1 AND is_active`, spreadCode).Scan(&spreadName, &positionsRaw); err != nil || strings.TrimSpace(spreadName) == "" {
		return nil, "", false
	}
	var positions []Position
	if err := json.Unmarshal(positionsRaw, &positions); err != nil || len(positions) != len(draws) {
		return nil, "", false
	}
	for _, position := range positions {
		if strings.TrimSpace(position.Label) == "" || strings.TrimSpace(position.Meaning) == "" {
			return nil, "", false
		}
	}
	return positions, spreadName, true
}

func (g *Gateway) persistTerminalFallback(pg *pgxpool.Pool, job *workerJob, text string) {
	if pg == nil || job == nil || job.id == "" || job.token == "" || strings.TrimSpace(text) == "" {
		return
	}
	status := "done"
	if ContainsStopWords(text) || text == SafeReplacement {
		text = SafeReplacement
		status = "filtered"
	}
	ctx, cancel := persistenceContext()
	defer cancel()
	_, _ = pg.Exec(ctx, `
		UPDATE readings
		   SET interpretation=$1, status=$2, worker_claim_token=NULL,
		       worker_lease_until=NULL, updated_at=now()
		 WHERE id=$3 AND worker_claim_token=$4
		   AND status IN ('pending', 'pending_fallback') AND quota_state='allowed'`, text, status, job.id, job.token)
}

func (g *Gateway) failClaim(pg *pgxpool.Pool, job *workerJob, policy workerPolicy) {
	if job.attempts >= policy.maxAttempts {
		g.markFailed(pg, job.id, job.token)
		return
	}
	g.releaseClaim(pg, job.id, job.token, policy.backoff(job.attempts))
}

func (g *Gateway) markFailed(pg *pgxpool.Pool, id, token string) {
	if pg == nil || id == "" || token == "" {
		return
	}
	ctx, cancel := persistenceContext()
	defer cancel()
	_, _ = pg.Exec(ctx, `
		UPDATE readings
		   SET status='failed', worker_claim_token=NULL, worker_lease_until=NULL, updated_at=now()
		 WHERE id=$1 AND worker_claim_token=$2
		   AND status IN ('pending', 'pending_fallback') AND quota_state='allowed'`, id, token)
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
		 WHERE id=$1 AND worker_claim_token=$2
		   AND status IN ('pending', 'pending_fallback') AND quota_state='allowed'`, id, token, seconds)
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
	text, _, _, err := g.Stream(ctx, readingID, spread, positions, cards, question, ch)
	if err != nil {
		return ""
	}
	for range ch {
	}
	return text
}
