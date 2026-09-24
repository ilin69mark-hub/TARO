// Package readings — флоу расклада (см. docs/project-book/02-functional/03).
// POST /v1/readings: validate → idempotency → entitlements.Check (consume) → draw → save.
// Шаффл: Fisher–Yates + Bernoulli(0.15) reversed, algo_version=1 (см. 03-database-schema.md).
// SSE: POST с Accept: text/event-stream стримит сохраненный текст; иначе JSON.
// Fallback-интерпретация из значений БД; настоящий AI-стрим втыкается в T13.
package readings

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"taro/api/internal/ai"
	"taro/api/internal/apierr"
	"taro/api/internal/auth"
	"taro/api/internal/entitlements"
	"taro/api/internal/referral"
)

var defaultCrisisPatterns = []string{
	"суицид", "самоубий", "покончить", "покончить с собой", "не хочу жить", "вскрыть вены",
	"повеситься", "убью себя", "убить себя", "нанесу себе вред", "передозиров",
}

const defaultCrisisResourceText = "Если ты думаешь о самоповреждении или о том, что можешь причинить себе вред, " +
	"пожалуйста, сейчас обратись в местные экстренные службы или на официальную линию кризисной поддержки " +
	"своей страны. Если есть непосредственная опасность, не оставайся один. Карты подождут."

const crisisText = defaultCrisisResourceText

var crisisPatterns = append([]string(nil), defaultCrisisPatterns...)

type crisisPolicy struct {
	patterns     []string
	resourceText string
}

func defaultCrisisPolicy() crisisPolicy {
	patterns := append([]string(nil), crisisPatterns...)
	if len(patterns) == 0 {
		patterns = append(patterns, defaultCrisisPatterns...)
	}
	return crisisPolicy{patterns: patterns, resourceText: defaultCrisisResourceText}
}

func normalizeCrisisText(value string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "ё", "е")
}

func crisisTextHasControl(value string) bool {
	for _, r := range value {
		if (r < 0x20 && r != '\n' && r != '\r' && r != '\t') || r == 0x7f {
			return true
		}
	}
	return false
}

func cleanCrisisPatterns(values []string) []string {
	seen := make(map[string]struct{}, len(defaultCrisisPatterns)+len(values))
	out := make([]string, 0, len(defaultCrisisPatterns)+len(values))
	for _, value := range append(append([]string(nil), defaultCrisisPatterns...), values...) {
		value = normalizeCrisisText(value)
		if value == "" || len([]rune(value)) > 100 || crisisTextHasControl(value) {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
		if len(out) >= 96 {
			break
		}
	}
	return out
}

func cleanCrisisResourceText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > 2000 || crisisTextHasControl(value) {
		return ""
	}
	return value
}

func (p crisisPolicy) matches(question string) bool {
	question = normalizeCrisisText(question)
	for _, pattern := range p.patterns {
		if strings.Contains(question, pattern) {
			return true
		}
	}
	return false
}

func (s *Service) loadCrisisPolicy(ctx context.Context) crisisPolicy {
	policy := defaultCrisisPolicy()
	if s.pg != nil {
		for _, key := range []string{"ai", "safety.crisis"} {
			var raw json.RawMessage
			if s.pg.QueryRow(ctx, `SELECT value FROM app_config WHERE key=$1`, key).Scan(&raw) != nil {
				continue
			}
			var value struct {
				CrisisPatterns     []string `json:"crisis_patterns"`
				CrisisResourceText string   `json:"crisis_resource_text"`
				Patterns           []string `json:"patterns"`
				ResourceText       string   `json:"resource_text"`
			}
			if json.Unmarshal(raw, &value) != nil {
				continue
			}
			patterns := append(value.CrisisPatterns, value.Patterns...)
			if len(patterns) > 0 {
				policy.patterns = cleanCrisisPatterns(patterns)
			}
			resource := value.CrisisResourceText
			if resource == "" {
				resource = value.ResourceText
			}
			if cleaned := cleanCrisisResourceText(resource); cleaned != "" {
				policy.resourceText = cleaned
			}
		}
	}
	if raw := strings.TrimSpace(os.Getenv("CRISIS_PATTERNS_JSON")); raw != "" {
		var patterns []string
		if json.Unmarshal([]byte(raw), &patterns) == nil {
			policy.patterns = cleanCrisisPatterns(patterns)
		}
	}
	if resource := cleanCrisisResourceText(os.Getenv("CRISIS_RESOURCE_TEXT")); resource != "" {
		policy.resourceText = resource
	}
	return policy
}

func isCrisisWithPolicy(question string, policy crisisPolicy) bool {
	return policy.matches(question)
}

// cardDraw — одна вытянутая карта.
type cardDraw struct {
	CardID   int  `json:"card_id"`
	Reversed bool `json:"reversed"`
	Position int  `json:"position"`
}

// Service — чтения (+AI через gateway; T12 fallback при отсутствии ключа).
type Service struct {
	pg *pgxpool.Pool
	en *entitlements.Service
	gw *ai.Gateway
	rf *referral.Service
}

// New возвращает сервис.
func New(pg *pgxpool.Pool, en *entitlements.Service, gw *ai.Gateway, rf *referral.Service) *Service {
	return &Service{pg: pg, en: en, gw: gw, rf: rf}
}

// fireReferralHook — хук рефералки после 1-го done-чтения (горутина, не блокирует).
// S06: recover — паника хука не роняет процесс.
func (s *Service) fireReferralHook(uid string) {
	if s.rf == nil {
		return
	}
	go func() {
		defer apierr.Recover()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var n int
		_ = s.pg.QueryRow(ctx,
			`SELECT COUNT(*) FROM readings WHERE user_id=$1 AND status='done'`, uid).Scan(&n)
		if n >= 1 {
			s.rf.CompleteOnFirstReading(ctx, uid)
		}
	}()
}

// draw тянет n карт: Fisher–Yates по 78 + reversed p=0.15. Детерминирован seed.
func draw(seed int64, n int) []cardDraw {
	if n < 0 {
		n = 0
	}
	if n > 78 {
		n = 78
	}
	rng := rand.New(rand.NewSource(seed))
	deck := make([]int, 78)
	for i := range deck {
		deck[i] = i
	}
	for i := len(deck) - 1; i > 0; i-- {
		j := rng.Intn(i + 1)
		deck[i], deck[j] = deck[j], deck[i]
	}
	out := make([]cardDraw, n)
	for i := 0; i < n; i++ {
		out[i] = cardDraw{CardID: deck[i], Reversed: rng.Float64() < 0.15, Position: i}
	}
	return out
}

func isCrisis(question string) bool {
	return isCrisisWithPolicy(question, defaultCrisisPolicy())
}

type createRequest struct {
	SpreadCode     string `json:"spread_code"`
	Question       string `json:"question"`
	IdempotencyKey string `json:"idempotency_key"` // fallback, главный — header
}

func validReadingKey(key string) bool {
	if key == "" || len(key) > 128 {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x21 || key[i] > 0x7e {
			return false
		}
	}
	return true
}

// HandleCreate — POST /v1/readings.
func (s *Service) HandleCreate(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	var req createRequest
	if !apierr.Decode(w, r, &req) {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		key = req.IdempotencyKey
	}
	if req.SpreadCode == "" || key == "" || !validReadingKey(key) || len(req.SpreadCode) > 32 {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нужны spread_code и Idempotency-Key")
		return
	}
	for i := 0; i < len(req.SpreadCode); i++ {
		if req.SpreadCode[i] < 0x21 || req.SpreadCode[i] > 0x7e {
			apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректный spread_code")
			return
		}
	}
	// S04: NUL-байты роняют PG (text запрещает \x00) → 422, не 500
	if strings.ContainsRune(req.Question, 0) {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректный вопрос")
		return
	}
	req.Question = strings.TrimSpace(req.Question)
	if utf8.RuneCountInString(req.Question) > 500 {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Вопрос длиннее 500 символов")
		return
	}
	ctx := r.Context()
	// Аудит C: locked-секция — в хелпере со своим conn (пул не висит всю SSE-сессию).
	id, _, cards, ok := s.prepareReading(w, r, uid, key, req)
	if !ok {
		return // ответ уже записан (повтор/paywall/кризис/ошибка)
	}
	// SSE + живой AI: стримим токены по мере генерации (tee в HTTP и в аккумулятор).
	// Иначе: догенерируем синхронно и отдаем JSON {reading_id}.
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") && s.gw != nil && s.gw.Enabled() {
		s.streamLive(w, r, id, req.SpreadCode, req.Question, cards)
		s.fireReferralHook(uid)
		return
	}
	s.generate(ctx, id, req.SpreadCode, req.Question, cards)
	s.respondReading(w, r, uid, id)
	s.fireReferralHook(uid)
}

// prepareReading — вся быстрая работа под advisory-lock на запиненном conn:
// find/insert pending → Check квоты → single-consume. Возвращает ok=false если
// ответ уже записан. Conn и lock освобождаются ДО долгой генерации (аудит C:
// иначе 10 висящих SSE съедают весь пул MaxConns=10).
func (s *Service) prepareReading(w http.ResponseWriter, r *http.Request, uid, key string, req createRequest) (string, entitlements.Verdict, []cardDraw, bool) {
	ctx := r.Context()
	policy := s.loadCrisisPolicy(ctx)
	conn, err := s.pg.Acquire(ctx)
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
		return "", entitlements.Verdict{}, nil, false
	}
	defer conn.Release()
	lockKey := "reading:" + uid + ":" + key
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext($1))`, lockKey); err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
		return "", entitlements.Verdict{}, nil, false
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext($1))`, lockKey)
	}()
	db := conn // все SQL секции — через запиненное соединение
	// спред активен? + число позиций
	var positions json.RawMessage
	var isPremium bool
	if err := db.QueryRow(ctx,
		`SELECT positions, is_premium FROM spreads WHERE code=$1 AND is_active`, req.SpreadCode).Scan(&positions, &isPremium); err != nil {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Неизвестный расклад")
		return "", entitlements.Verdict{}, nil, false
	}
	var posCount []any
	if err := json.Unmarshal(positions, &posCount); err != nil || len(posCount) == 0 || len(posCount) > 78 {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Битый расклад")
		return "", entitlements.Verdict{}, nil, false
	}
	// идемпотентность ДО списания лимита: повтор возвращает существующее.
	// Мёртвые строки (cancelled/failed) — удаляем, ключ освобождается под fresh-вставку.
	if existing := s.findByKey(ctx, uid, key); existing != "" {
		var st string
		if err := db.QueryRow(ctx, `SELECT status FROM readings WHERE id=$1`, existing).Scan(&st); err != nil || st == "cancelled" || st == "failed" {
			_, _ = db.Exec(ctx, `DELETE FROM readings WHERE id=$1`, existing)
		} else {
			s.respondReading(w, r, uid, existing)
			return "", entitlements.Verdict{}, nil, false
		}
	}
	if isCrisisWithPolicy(req.Question, policy) {
		id := s.createFilteredText(ctx, w, uid, req, key, len(posCount), policy.resourceText)
		if id == "" {
			return "", entitlements.Verdict{}, nil, false
		}
		s.respondReading(w, r, uid, id)
		return "", entitlements.Verdict{}, nil, false
	}
	seed := time.Now().UnixNano()
	cards := draw(seed, len(posCount))
	cardsJSON, _ := json.Marshal(cards)

	// Аудит B: вставляем pending ДО списания квоты — падение вставки не сжигает лимит.
	// Повторный конкурент заблокирован advisory-lock выше, ON CONFLICT — пояс поверх.
	var id string
	err = db.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, interpretation, seed, algo_version, status, idempotency_key, worker_lease_until)
		VALUES ($1,$2,$3,$4,'', $5,1,'pending',$6,now()+interval '45 seconds')
		ON CONFLICT (user_id, idempotency_key) DO NOTHING
		RETURNING id`, uid, req.SpreadCode, req.Question, cardsJSON, seed, key).Scan(&id)
	if err != nil {
		// race: второй запрос вставил первым — возвращаем его
		if existing := s.findByKey(ctx, uid, key); existing != "" {
			s.respondReading(w, r, uid, existing)
			return "", entitlements.Verdict{}, nil, false
		}
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось сохранить расклад")
		return "", entitlements.Verdict{}, nil, false
	}
	// лимиты (consume внутри Check) — ПОСЛЕ вставки; отказ → строка в cancelled, квота цела
	v, err := s.en.Check(ctx, uid, req.SpreadCode)
	if err != nil {
		_, _ = db.Exec(ctx, `UPDATE readings SET status='failed', worker_claim_token=NULL, worker_lease_until=NULL WHERE id=$1`, id)
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось проверить лимит")
		return "", entitlements.Verdict{}, nil, false
	}
	if !v.Allow {
		_, _ = db.Exec(ctx, `UPDATE readings SET status='cancelled', worker_claim_token=NULL, worker_lease_until=NULL WHERE id=$1`, id)
		s.writePaywall(w, r)
		return "", entitlements.Verdict{}, nil, false
	}
	if v.Reason == "single" {
		tag, err := db.Exec(ctx,
			`UPDATE single_entitlements SET consumed_reading_id=$1 WHERE id=$2 AND consumed_reading_id IS NULL`, id, v.SingleID)
		if err != nil || tag.RowsAffected() == 0 {
			// race: второй запрос потребил single первым — откатываем pending и просим оплатить.
			// Статус cancelled легален с миграции 018; ошибку Exec не глотаем, а проверяем.
			if _, uerr := db.Exec(ctx, `UPDATE readings SET status='cancelled', worker_claim_token=NULL, worker_lease_until=NULL WHERE id=$1`, id); uerr != nil {
				apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось сохранить расклад")
				return "", entitlements.Verdict{}, nil, false
			}
			s.writePaywall(w, r)
			return "", entitlements.Verdict{}, nil, false
		}
	}
	return id, v, cards, true
}

func independentPersistenceContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

func (s *Service) streamLive(w http.ResponseWriter, r *http.Request, id, spreadCode, question string, cards []cardDraw) {
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	positions, values, spreadName := s.aiInputs(ctx, spreadCode, cards)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	fl, _ := w.(http.Flusher)
	ch := make(chan string, 256)
	type res struct {
		text  string
		model string
		err   error
	}
	done := make(chan res, 1)
	go func() {
		started := false
		defer func() {
			if recovered := recover(); recovered != nil {
				if !started {
					close(ch)
				}
				logCtx, logCancel := independentPersistenceContext()
				_, _ = s.pg.Exec(logCtx,
					`INSERT INTO ai_logs (reading_id, model, prompt_hash, status, error) VALUES ($1,'', '', 'failed', $2)`,
					id, fmt.Sprintf("stream panic: %v", recovered))
				logCancel()
				done <- res{"", "", fmt.Errorf("stream panic: %v", recovered)}
			}
		}()
		started = true
		text, model, _, err := s.gw.Stream(ctx, id, spreadName, positions, values, question, ch)
		done <- res{text, model, err}
	}()
	var sb strings.Builder
	failed := false
	for token := range ch {
		sb.WriteString(token)
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]string{"token": token}))
		if fl != nil {
			fl.Flush()
		}
	}
	gen := <-done
	final := gen.text
	if final == "" {
		final = sb.String()
	}
	status := "done"
	persistCtx, persistCancel := independentPersistenceContext()
	defer persistCancel()
	if gen.err != nil || strings.TrimSpace(final) == "" {
		failed = true
		final = s.fallbackText(persistCtx, spreadCode, cards) + "\n\nПолное толкование допишется автоматически."
		status = "pending_fallback"
	} else if ai.ContainsStopWords(final) || final == ai.SafeReplacement {
		final = ai.SafeReplacement
		status = "filtered"
	}
	_, _ = s.pg.Exec(persistCtx,
		`UPDATE readings SET interpretation=$1, status=$2, worker_claim_token=NULL, worker_lease_until=NULL, updated_at=now() WHERE id=$3 AND status IN ('pending', 'pending_fallback') AND worker_claim_token IS NULL`,
		final, status, id)
	if failed {
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]any{"fallback": true}))
	}
	fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]any{"done": true, "reading_id": id, "status": status}))
}

func (s *Service) generate(ctx context.Context, id, spreadCode, question string, cards []cardDraw) {
	if s.gw == nil || !s.gw.Enabled() {
		persistCtx, persistCancel := independentPersistenceContext()
		_, _ = s.pg.Exec(persistCtx,
			`UPDATE readings SET interpretation=$1, status='done', worker_claim_token=NULL, worker_lease_until=NULL WHERE id=$2 AND status IN ('pending', 'pending_fallback') AND worker_claim_token IS NULL`,
			s.fallbackText(persistCtx, spreadCode, cards), id)
		persistCancel()
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	positions, values, spreadName := s.aiInputs(ctx, spreadCode, cards)
	ch := make(chan string, 256)
	type res struct {
		text string
		err  error
	}
	done := make(chan res, 1)
	go func() {
		started := false
		defer func() {
			if recovered := recover(); recovered != nil {
				if !started {
					close(ch)
				}
				logCtx, logCancel := independentPersistenceContext()
				_, _ = s.pg.Exec(logCtx,
					`INSERT INTO ai_logs (reading_id, model, prompt_hash, status, error) VALUES ($1,'', '', 'failed', $2)`,
					id, fmt.Sprintf("stream panic: %v", recovered))
				logCancel()
				done <- res{"", fmt.Errorf("stream panic: %v", recovered)}
			}
		}()
		started = true
		text, _, _, err := s.gw.Stream(ctx, id, spreadName, positions, values, question, ch)
		done <- res{text, err}
	}()
	for range ch {
	}
	result := <-done
	persistCtx, persistCancel := independentPersistenceContext()
	defer persistCancel()
	if result.err != nil || strings.TrimSpace(result.text) == "" {
		_, _ = s.pg.Exec(persistCtx,
			`UPDATE readings SET interpretation=$1, status='pending_fallback', worker_claim_token=NULL, worker_lease_until=NULL, updated_at=now() WHERE id=$2 AND status IN ('pending', 'pending_fallback') AND worker_claim_token IS NULL`,
			s.fallbackText(persistCtx, spreadCode, cards)+"\n\nПолное толкование допишется автоматически.", id)
		return
	}
	text := result.text
	status := "done"
	if ai.ContainsStopWords(text) || text == ai.SafeReplacement {
		text = ai.SafeReplacement
		status = "filtered"
	}
	_, _ = s.pg.Exec(persistCtx,
		`UPDATE readings SET interpretation=$1, status=$2, worker_claim_token=NULL, worker_lease_until=NULL, updated_at=now() WHERE id=$3 AND status IN ('pending', 'pending_fallback') AND worker_claim_token IS NULL`,
		text, status, id)
}

// aiInputs собирает позиции и значения карт для промпта.
func (s *Service) aiInputs(ctx context.Context, spreadCode string, cards []cardDraw) ([]ai.Position, []ai.CardValue, string) {
	var spreadName string
	var posRaw json.RawMessage
	_ = s.pg.QueryRow(ctx, `SELECT name_ru, positions FROM spreads WHERE code=$1`, spreadCode).Scan(&spreadName, &posRaw)
	var positions []ai.Position
	_ = json.Unmarshal(posRaw, &positions)
	values := make([]ai.CardValue, 0, len(cards))
	for _, c := range cards {
		var name, up, rev string
		if err := s.pg.QueryRow(ctx,
			`SELECT name_ru, upright_ru, reversed_ru FROM cards WHERE id=$1`, c.CardID).Scan(&name, &up, &rev); err != nil {
			continue
		}
		values = append(values, ai.CardValue{Name: name, Upright: up, ReversedText: rev, Reversed: c.Reversed, Position: c.Position, CardID: c.CardID})
	}
	return positions, values, spreadName
}

func (s *Service) createFiltered(ctx context.Context, w http.ResponseWriter, uid string, req createRequest, key string, n int) string {
	return s.createFilteredText(ctx, w, uid, req, key, n, crisisText)
}

func (s *Service) createFilteredText(ctx context.Context, w http.ResponseWriter, uid string, req createRequest, key string, n int, resourceText string) string {
	if cleaned := cleanCrisisResourceText(resourceText); cleaned != "" {
		resourceText = cleaned
	} else {
		resourceText = defaultCrisisResourceText
	}
	seed := time.Now().UnixNano()
	cards := draw(seed, n)
	cardsJSON, _ := json.Marshal(cards)
	var id string
	err := s.pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, interpretation, seed, algo_version, status, idempotency_key)
		VALUES ($1,$2,$3,$4,$5,$6,1,'filtered',$7)
		ON CONFLICT (user_id, idempotency_key) DO NOTHING
		RETURNING id`, uid, req.SpreadCode, req.Question, cardsJSON, resourceText, seed, key).Scan(&id)
	if err != nil {
		if existing := s.findByKey(ctx, uid, key); existing != "" {
			return existing
		}
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось сохранить расклад")
		return ""
	}
	return id
}

// findByKey ищет reading по (user, key).
func (s *Service) findByKey(ctx context.Context, uid, key string) string {
	var id string
	if err := s.pg.QueryRow(ctx,
		`SELECT id FROM readings WHERE user_id=$1 AND idempotency_key=$2`, uid, key).Scan(&id); err != nil {
		return ""
	}
	return id
}

// respondReading: SSE при Accept: text/event-stream, иначе JSON {reading_id}.
func (s *Service) respondReading(w http.ResponseWriter, r *http.Request, uid, id string) {
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		s.streamReading(w, r, uid, id)
		return
	}
	var status string
	if err := s.pg.QueryRow(r.Context(), `SELECT status FROM readings WHERE id=$1 AND user_id=$2`, id, uid).Scan(&status); err == nil &&
		(status == "pending" || status == "pending_fallback") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"reading_id": id, "status": status})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"reading_id": id})
}

// streamReading стримит сохраненный текст токенами-словами (T13: живые токены OpenRouter).
// Аудит B: фильтр по владельцу в SQL (было: любой id без проверки — спящий IDOR).
func (s *Service) streamReading(w http.ResponseWriter, r *http.Request, uid, id string) {
	var text, status string
	if err := s.pg.QueryRow(r.Context(),
		`SELECT COALESCE(left(interpretation, $3), ''), status FROM readings WHERE id=$1 AND user_id=$2`, id, uid, ai.MaxOutputBytes+1).Scan(&text, &status); err != nil {
		apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Расклад не найден")
		return
	}
	if ai.ContainsStopWords(text) {
		text = ai.SafeReplacement
		status = "filtered"
	}
	if len(text) > ai.MaxOutputBytes {
		text = ai.SafeReplacement
		status = "filtered"
	}
	if strings.TrimSpace(text) == "" && (status == "pending" || status == "pending_fallback") {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		_, _ = fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]any{"pending": true, "reading_id": id, "status": status}))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	fl, _ := w.(http.Flusher)
	for _, word := range strings.Split(text, " ") {
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]string{"token": word + " "}))
		if fl != nil {
			fl.Flush()
		}
	}
	fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]any{"done": true, "reading_id": id, "status": status}))
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// fallbackText собирает толкование из значений карт БД (без AI).
func (s *Service) fallbackText(ctx context.Context, spreadCode string, cards []cardDraw) string {
	var sb strings.Builder
	sb.WriteString("Карты вытянуты. Полное AI-толкование появится в T13 — а пока значения карт:\n")
	for _, c := range cards {
		var name, up, rev string
		if err := s.pg.QueryRow(ctx,
			`SELECT name_ru, upright_ru, reversed_ru FROM cards WHERE id=$1`, c.CardID).Scan(&name, &up, &rev); err != nil {
			continue
		}
		val := up
		orient := "прямая"
		if c.Reversed {
			val = rev
			orient = "перевернутая"
		}
		fmt.Fprintf(&sb, "\n• %s (%s, позиция %d): %s", name, orient, c.Position+1, val)
	}
	return sb.String()
}

// writePaywall — 402 с активными планами из конфига (цены не хардкод).
func (s *Service) writePaywall(w http.ResponseWriter, r *http.Request) {
	type plan struct {
		Code  string `json:"code"`
		Price int    `json:"price_rub"`
		Stars int    `json:"stars_amount"`
	}
	rows, err := s.pg.Query(r.Context(),
		`SELECT code, price_rub, stars_amount
		 FROM (
			SELECT DISTINCT ON (code) code, price_rub, stars_amount, is_active
			FROM plans WHERE code != 'free'
			ORDER BY code, valid_from DESC
		 ) latest
		 WHERE latest.is_active ORDER BY price_rub`)
	if err != nil {
		apierr.Write(w, 402, apierr.CodeLimitSkip, "На сегодня бесплатные карты закончились")
		return
	}
	defer rows.Close()
	plans := []plan{}
	for rows.Next() {
		var p plan
		_ = rows.Scan(&p.Code, &p.Price, &p.Stars)
		plans = append(plans, p)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(402)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":   map[string]string{"code": apierr.CodeLimitSkip, "message_ru": "На сегодня бесплатные карты закончились — продолжим завтра или безлимитно?"},
		"paywall": plans,
	})
}

// HandleList — GET /v1/readings?limit&offset&q (q только premium, иначе 403).
func (s *Service) HandleList(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	ctx := r.Context()
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if err != nil || offset < 0 || offset > 10000 {
		if r.URL.Query().Get("offset") != "" {
			apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректный offset")
			return
		}
		offset = 0
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if utf8.RuneCountInString(q) > 100 {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Слишком длинный поиск")
		return
	}
	// Аудит D: экранируем wildcards (q=% матчил всё) — ESCAPE '\'.
	q = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	if q != "" {
		var premium bool
		_ = s.pg.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM subscriptions WHERE user_id=$1 AND status='active' AND valid_until > now())`,
			uid).Scan(&premium)
		if !premium {
			apierr.Write(w, http.StatusForbidden, apierr.CodeForbidden, "Поиск — для premium")
			return
		}
	}
	rows, err := s.pg.Query(ctx, `
		SELECT id, spread_code, question, left(interpretation, $5), created_at
		  FROM readings WHERE user_id=$1 AND status NOT IN ('cancelled','failed') AND ($3='' OR question ILIKE '%'||$3||'%' ESCAPE '\\')
		 ORDER BY created_at DESC LIMIT $2 OFFSET $4`, uid, limit, q, offset, ai.MaxOutputBytes+1)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось загрузить историю")
		return
	}
	defer rows.Close()
	type item struct {
		ID        string `json:"id"`
		Spread    string `json:"spread"`
		Question  string `json:"question"`
		Preview   string `json:"preview"`
		CreatedAt string `json:"created_at"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		var qn, pv *string
		var ts time.Time
		if err := rows.Scan(&it.ID, &it.Spread, &qn, &pv, &ts); err != nil {
			continue
		}
		if qn != nil {
			it.Question = *qn
		}
		if pv != nil {
			it.Preview = *pv
		}
		if ai.ContainsStopWords(it.Preview) {
			it.Preview = ai.SafeReplacement
		}
		if len(it.Preview) > 160 {
			it.Preview = it.Preview[:160]
		}
		it.CreatedAt = ts.Format(time.RFC3339)
		out = append(out, it)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// HandleGet — GET /v1/readings/:id. Free видит полный текст только сегодняшних.
func (s *Service) HandleGet(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	id := chi.URLParam(r, "id")
	ctx := r.Context()
	var spread, question, interp, status string
	var cards json.RawMessage
	var created time.Time
	// Аудит B: владелец фильтруется в SQL, чужая строка не читается вообще.
	err := s.pg.QueryRow(ctx, `
		SELECT spread_code, question, cards, COALESCE(left(interpretation, $3), ''), status, created_at
		  FROM readings WHERE id=$1 AND user_id=$2`, id, uid, ai.MaxOutputBytes+1).Scan(&spread, &question, &cards, &interp, &status, &created)
	if err != nil {
		apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Расклад не найден")
		return
	}
	if ai.ContainsStopWords(interp) {
		interp = ai.SafeReplacement
		status = "filtered"
	}
	if len(interp) > ai.MaxOutputBytes {
		interp = ai.SafeReplacement
		status = "filtered"
	}
	// обогащаем карты именами из БД (UI не ходит за именами отдельно, T16)
	cards = s.enrichCards(ctx, cards)
	var premium bool
	_ = s.pg.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM subscriptions WHERE user_id=$1 AND status='active' AND valid_until > now())`,
		uid).Scan(&premium)
	locked := false
	if !premium && !sameMSKDay(created, time.Now()) {
		interp = ""
		locked = true
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": id, "spread": spread, "question": question, "cards": cards,
		"interpretation": interp, "locked": locked, "status": status,
		"created_at": created.Format(time.RFC3339),
	})
}

// enrichCards добавляет name_ru к каждой карте (N≤10, точечные SELECT).
func (s *Service) enrichCards(ctx context.Context, raw json.RawMessage) json.RawMessage {
	var draws []map[string]any
	if json.Unmarshal(raw, &draws) != nil {
		return raw
	}
	for _, d := range draws {
		idf, ok := d["card_id"].(float64)
		if !ok {
			continue
		}
		var name, key string
		if err := s.pg.QueryRow(ctx, `SELECT name_ru, image_key FROM cards WHERE id=$1`, int(idf)).Scan(&name, &key); err == nil {
			d["name_ru"] = name
			d["image_key"] = key
		}
	}
	out, err := json.Marshal(draws)
	if err != nil {
		return raw
	}
	return out
}

// HandleStreak — GET /v1/streak/me: дней подряд с ≥1 done-чтением ИЛИ записью дневника.
// (MSK, см. U20/V11). Считается из readings+diary_entries, миграций не требует.
func (s *Service) HandleStreak(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	var days []string
	rows, err := s.pg.Query(r.Context(), `
		SELECT DISTINCT d::text FROM (
		  SELECT (created_at AT TIME ZONE 'Europe/Moscow')::date AS d
		    FROM readings WHERE user_id=$1 AND status='done'
		  UNION
		  SELECT (created_at AT TIME ZONE 'Europe/Moscow')::date AS d
		    FROM diary_entries WHERE user_id=$1
		) t ORDER BY 1 DESC LIMIT 370`, uid)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось посчитать стрик")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		_ = rows.Scan(&d)
		days = append(days, d)
	}
	streak := 0
	today := time.Now().In(msk()).Format("2006-01-02")
	cur := today
	hasToday := len(days) > 0 && days[0] == today
	start := 0
	if !hasToday {
		// стрик жив если вчера был день: начинаем со вчера
		y := time.Now().In(msk()).AddDate(0, 0, -1).Format("2006-01-02")
		if len(days) == 0 || days[0] != y {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"days": 0})
			return
		}
		cur = y
		start = 0
	}
	for i := start; i < len(days); i++ {
		if days[i] != cur {
			break
		}
		streak++
		t, _ := time.Parse("2006-01-02", cur)
		cur = t.AddDate(0, 0, -1).Format("2006-01-02")
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"days": streak})
}

func msk() *time.Location {
	msk, _ := time.LoadLocation("Europe/Moscow")
	if msk == nil {
		msk = time.FixedZone("MSK", 3*3600)
	}
	return msk
}

// sameMSKDay — один ли календарный день МСК.
func sameMSKDay(a, b time.Time) bool {
	msk, _ := time.LoadLocation("Europe/Moscow")
	if msk == nil {
		msk = time.FixedZone("MSK", 3*3600)
	}
	aa, bb := a.In(msk), b.In(msk)
	return aa.Year() == bb.Year() && aa.YearDay() == bb.YearDay()
}
