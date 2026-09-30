package ai

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"taro/api/internal/entitlements"
)

const (
	defaultWorkerMaxAttempts = 5
	defaultWorkerBackoff     = 30 * time.Second
	defaultWorkerMaxBackoff  = 15 * time.Minute
	workerProcessingTimeout  = 30 * time.Second
	workerLeaseDuration      = 2 * time.Minute
	// A15/F-06: потолок воркера был 1 чтение за 30 с = 2.00 readings/min
	// (жёсткие batch=1 и tick=30s). Теперь расписание конфигурируемо, по
	// умолчанию 10 чтений за 2 с, а обработанный батч обрабатывается ПАРАЛЛЕЛЬНО:
	// последовательный цикл по 10 строкам при генерации по 5 с давал бы те же
	// 0.2 чтения/с.
	defaultWorkerBatchSize = 10
	defaultWorkerTick      = 2 * time.Second
	minWorkerTick          = 1 * time.Second
	maxWorkerTick          = 60 * time.Second
	// workerFailedReason — машиночитаемая причина терминального провала.
	// Наружу отдаётся только код: текст ошибки провайдера может содержать
	// детали запроса/ключа и клиенту не нужен (A12/F-12).
	workerFailedReason = "provider_failed"
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
	// batchSize — сколько чтений воркер захватывает за один drain. Он же
	// ограничивает параллелизм обработки: отдельного параметра concurrency нет
	// намеренно, иначе «захватили 10, обработали 1» снова даст потолок.
	batchSize int
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
		batchSize:   envInt("AI_WORKER_BATCH_SIZE", defaultWorkerBatchSize, 1, 50),
	}
	if policy.maxBackoff < policy.baseBackoff {
		policy.maxBackoff = policy.baseBackoff
	}
	return policy
}

// WorkerTick — экспортная обёртка tick'а для точки сборки (cmd/api). Держим
// публичным, чтобы main.go не знал про дефолты, а потолок воркера менялся
// одной правкой здесь (A15/F-06).
func WorkerTick() time.Duration { return workerTick() }

// workerTick — период ожидания воркера, когда очередь ПУСТА. A15/F-06: раньше
// здесь стояло жёсткое 30*time.Second в cmd/api/main.go, и вместе с batch=1
// это давало жёсткий потолок 2.00 readings/min: за 30 секунд пользователь
// ждал ровно одно чтение независимо от того, что очередь пуста.
func workerTick() time.Duration {
	seconds := envInt("AI_WORKER_TICK_SECONDS", int(defaultWorkerTick/time.Second),
		int(minWorkerTick/time.Second), int(maxWorkerTick/time.Second))
	return time.Duration(seconds) * time.Second
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

// drainStats — наблюдаемость A15/F-06. В проекте нет метрик-стека (нет
// prometheus), поэтому глубина очереди и факт обработки уходят в лог входа
// плюс возвращаются вызывающему: без этого потолок воркера невидим.
type drainStats struct {
	claimed    int
	persisted  int
	queueDepth int
	batchSize  int
	exhausted  int
}

// full сообщает, что батч забран целиком — значит очередь, скорее всего, не
// пуста и ждать следующего тика незачем.
func (s drainStats) full() bool { return s.batchSize > 0 && s.claimed >= s.batchSize }

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
			stats := drainStats{}
			func() {
				defer func() { _ = recover() }()
				stats = g.drainOnce(ctx, pg)
			}()
			if ctx.Err() != nil {
				return
			}
			// A15/F-06: тик — это период ожидания ПУСТОЙ очереди, а не темп
			// обработки. Если батч забран целиком, дренируем сразу: иначе
			// 10 чтений ждали бы следующего тика каждое.
			if stats.claimed > 0 || stats.exhausted > 0 {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

func (g *Gateway) drainOnce(ctx context.Context, pg *pgxpool.Pool) drainStats {
	defer func() { _ = recover() }()
	stats := drainStats{batchSize: loadWorkerPolicy().batchSize}
	if pg == nil {
		return stats
	}
	policy := loadWorkerPolicy()
	stats.batchSize = policy.batchSize
	recovery := entitlements.New(pg, g.rd)
	_ = recovery.RecoverPendingAuthorizations(ctx, 20)
	// A12/F-12: те же правила освобождения, что и в markFailed — массовый
	// переход «попытки исчерпаны» тоже обязан вернуть entitlement.
	exhausted, exhaustedErr := pg.Query(ctx, `
		UPDATE readings
		   SET status='failed', failure_reason=$2, failed_at=now(),
		       worker_claim_token=NULL, worker_lease_until=NULL, updated_at=now()
		 WHERE status IN ('pending', 'pending_fallback')
		   AND quota_state='allowed'
		   AND worker_attempts >= $1
		   AND (worker_lease_until IS NULL OR worker_lease_until <= now())
		RETURNING id::text`, policy.maxAttempts, workerFailedReason)
	if exhaustedErr != nil {
		log.Printf("ai worker: exhausted transition: %v", exhaustedErr)
	}
	if exhausted != nil {
		var ids []string
		for exhausted.Next() {
			var id string
			if exhausted.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		exhausted.Close()
		stats.exhausted = len(ids)
		for _, id := range ids {
			if _, err := recovery.ReleaseReadingAuthorization(ctx, id); err != nil {
				log.Printf("ai worker: release authorization for failed reading %s: %v", id, err)
			}
		}
	}
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
		RETURNING r.id::text, r.spread_code, COALESCE(r.question,''), r.cards, r.worker_claim_token::text, r.worker_attempts`, policy.maxAttempts, policy.batchSize, int(workerLeaseDuration/time.Second))
	if err != nil {
		return stats
	}
	jobs := make([]workerJob, 0, policy.batchSize)
	for rows.Next() {
		var job workerJob
		if err := rows.Scan(&job.id, &job.spread, &job.question, &job.cardsRaw, &job.token, &job.attempts); err == nil {
			jobs = append(jobs, job)
		}
	}
	rows.Close()
	stats.claimed = len(jobs)
	stats.queueDepth = pendingQueueDepth(ctx, pg)
	if len(jobs) == 0 {
		return stats
	}

	// A15/F-06: параллельная обработка батча. Каждая задача имеет собственный
	// claim-токен и собственный контекст, поэтому запись в БД по-прежнему
	// защищена `worker_claim_token=$4` и две обработки одного чтения
	// невозможны. Порядок не важен: у каждой строки своя транзакция.
	var wg sync.WaitGroup
	persisted := make([]bool, len(jobs))
	for i := range jobs {
		job := &jobs[i]
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			defer func() { _ = recover() }()
			persisted[index] = g.processClaim(ctx, pg, job, policy)
		}(i)
	}
	wg.Wait()
	for _, ok := range persisted {
		if ok {
			stats.persisted++
		}
	}
	log.Printf("ai worker: drain claimed=%d persisted=%d queue=%d batch=%d exhausted=%d",
		stats.claimed, stats.persisted, stats.queueDepth, stats.batchSize, stats.exhausted)
	return stats
}

// pendingQueueDepth — сколько чтений ждут воркера. Считается по партиальному
// индексу idx_readings_worker_quota (index-only scan).
func pendingQueueDepth(ctx context.Context, pg *pgxpool.Pool) int {
	if pg == nil {
		return 0
	}
	var depth int
	if err := pg.QueryRow(ctx, `
		SELECT count(*) FROM readings
		 WHERE status IN ('pending', 'pending_fallback')
		   AND quota_state='allowed'`).Scan(&depth); err != nil {
		return -1
	}
	return depth
}

// processClaim обрабатывает одно захваченное чтение. Возвращает true, если
// результат записан в БД (done/filtered), false — если чтение отпущено с
// backoff или помечено failed.
func (g *Gateway) processClaim(ctx context.Context, pg *pgxpool.Pool, job *workerJob, policy workerPolicy) bool {
	jobCtx, cancel := workerJobContext(ctx)
	cards, ok := loadWorkerCards(jobCtx, pg, job.cardsRaw)
	if !ok {
		cancel()
		g.failClaim(pg, job, policy)
		return false
	}
	job.cards = cards
	positions, spread, complete := loadWorkerSpread(jobCtx, pg, job.cardsRaw, job.spread)
	if !complete || !g.Enabled() {
		cancel()
		g.persistTerminalFallback(pg, job, FallbackInterpretation(cards))
		return true
	}
	job.positions = positions
	job.spread = spread
	text := g.completeReading(jobCtx, job.id, job.spread, job.positions, job.cards, job.question)
	cancel()
	if text == "" {
		g.failClaim(pg, job, policy)
		return false
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
		return false
	}
	return true
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
	tag, err := pg.Exec(ctx, `
		UPDATE readings
		   SET status='failed', failure_reason=$3, failed_at=now(),
		       worker_claim_token=NULL, worker_lease_until=NULL, updated_at=now()
		 WHERE id=$1 AND worker_claim_token=$2
		   AND status IN ('pending', 'pending_fallback') AND quota_state='allowed'`, id, token, workerFailedReason)
	if err != nil {
		// Раньше ошибка молча терялась, и провал выглядел как «воркер не тронул
		// строку»: слот оставался списанным, а чтение — pending навсегда.
		log.Printf("ai worker: markFailed(%s): %v", id, err)
		return
	}
	// A12/F-12: списание entitlement было сделано ДО вызова провайдера, поэтому
	// терминальный провал обязан вернуть слот — иначе пользователь теряет
	// бесплатное чтение (или оплаченный single) за неоказанную услугу.
	if tag.RowsAffected() == 1 {
		recovery := entitlements.New(pg, g.rd)
		if _, err := recovery.ReleaseReadingAuthorization(ctx, id); err != nil {
			log.Printf("ai worker: release authorization for failed reading %s: %v", id, err)
		}
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
		 WHERE id=$1 AND worker_claim_token=$2
		   AND status IN ('pending', 'pending_fallback') AND quota_state='allowed'`, id, token, seconds)
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
