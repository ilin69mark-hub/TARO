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

// crisisPatterns — грубый фильтр: суицид/самоповреждение → шаблон без AI (см. 08-risks/02).
var crisisPatterns = []string{"суицид", "покончить", "вскрыть вены", "повеситься", "убью себя", "суицид"}

const crisisText = "Если тебе тяжело и приходят мысли о самоповреждении — " +
	"пожалуйста, обратись за помощью: телефон доверия 8-800-7000-600 (круглосуточно, бесплатно). " +
	"Карты подождут. Ты важен."

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
		ctx := context.Background()
		var n int
		_ = s.pg.QueryRow(ctx,
			`SELECT COUNT(*) FROM readings WHERE user_id=$1 AND status='done'`, uid).Scan(&n)
		if n == 1 {
			s.rf.CompleteOnFirstReading(ctx, uid)
		}
	}()
}

// draw тянет n карт: Fisher–Yates по 78 + reversed p=0.15. Детерминирован seed.
func draw(seed int64, n int) []cardDraw {
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

// isCrisis — фильтр запрещенки (нижний регистр, подстроки).
func isCrisis(question string) bool {
	q := strings.ToLower(question)
	for _, p := range crisisPatterns {
		if strings.Contains(q, p) {
			return true
		}
	}
	return false
}

type createRequest struct {
	SpreadCode     string `json:"spread_code"`
	Question       string `json:"question"`
	IdempotencyKey string `json:"idempotency_key"` // fallback, главный — header
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
	if req.SpreadCode == "" || key == "" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нужны spread_code и Idempotency-Key")
		return
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
	// спред активен? + число позиций
	var positions json.RawMessage
	var isPremium bool
	if err := s.pg.QueryRow(ctx,
		`SELECT positions, is_premium FROM spreads WHERE code=$1 AND is_active`, req.SpreadCode).Scan(&positions, &isPremium); err != nil {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Неизвестный расклад")
		return
	}
	var posCount []any
	if err := json.Unmarshal(positions, &posCount); err != nil || len(posCount) == 0 {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Битый расклад")
		return
	}
	// идемпотентность ДО списания лимита: повтор возвращает существующее
	if existing := s.findByKey(ctx, uid, key); existing != "" {
		s.respondReading(w, r, uid, existing)
		return
	}
	// кризис — до лимитов и без списания: безопасность важнее квоты
	if isCrisis(req.Question) {
		id := s.createFiltered(ctx, w, uid, req, key, len(posCount))
		if id == "" {
			return
		}
		s.respondReading(w, r, uid, id)
		return
	}
	// лимиты (consume внутри Check)
	v, err := s.en.Check(ctx, uid, req.SpreadCode)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось проверить лимит")
		return
	}
	if !v.Allow {
		s.writePaywall(w, r)
		return
	}
	seed := time.Now().UnixNano()
	cards := draw(seed, len(posCount))
	cardsJSON, _ := json.Marshal(cards)

	// вставляем pending: строка существует до генерации (SSE-обрыв → докачка/воркер)
	var id string
	err = s.pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, interpretation, seed, algo_version, status, idempotency_key)
		VALUES ($1,$2,$3,$4,'', $5,1,'pending',$6)
		ON CONFLICT (user_id, idempotency_key) DO NOTHING
		RETURNING id`, uid, req.SpreadCode, req.Question, cardsJSON, seed, key).Scan(&id)
	if err != nil {
		// race: второй запрос вставил первым — возвращаем его
		if existing := s.findByKey(ctx, uid, key); existing != "" {
			s.respondReading(w, r, uid, existing)
			return
		}
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось сохранить расклад")
		return
	}
	if v.Reason == "single" {
		_, _ = s.pg.Exec(ctx,
			`UPDATE single_entitlements SET consumed_reading_id=$1 WHERE id=$2 AND consumed_reading_id IS NULL`, id, v.SingleID)
	}
	// SSE + живой AI: стримим токены по мере генерации (tee в HTTP и в аккумулятор).
	// Иначе: догенерируем синхронно и отдаем JSON {reading_id}.
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") && s.gw != nil && s.gw.Enabled() {
		s.streamLive(w, r, id, req.SpreadCode, req.Question, cards)
		s.fireReferralHook(uid)
		return
	}
	s.generate(ctx, id, req.SpreadCode, req.Question, cards)
	_ = isPremium
	s.respondReading(w, r, uid, id)
	s.fireReferralHook(uid)
}

// streamLive — живой SSE-стрим генерации: токены клиенту + сохранение в конце.
// Обрыв клиентом (ctx cancel) → строка остается pending_fallback, допишет worker.
// S06: общий дедлайн 25с (см. 02-interaction-next-go.md: 8с×2 + total SSE).
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
		defer apierr.Recover() // S06
		text, model, _, err := s.gw.Stream(ctx, id, spreadName, positions, values, question, ch)
		done <- res{text, model, err}
	}()
	var sb strings.Builder
	failed := false
	for t := range ch {
		sb.WriteString(t)
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]string{"token": t}))
		if fl != nil {
			fl.Flush()
		}
	}
	gen := <-done
	status := "done"
	final := sb.String()
	if gen.err != nil {
		failed = true
		final = s.fallbackText(r.Context(), spreadCode, cards) + "\n\nПолное толкование допишется автоматически."
		status = "pending_fallback"
	} else if ai.ContainsStopWords(final) {
		final = ai.SafeReplacement
		status = "filtered"
	}
	_, _ = s.pg.Exec(r.Context(),
		`UPDATE readings SET interpretation=$1, status=$2 WHERE id=$3`, final, status, id)
	if failed {
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]any{"fallback": true}))
	}
	fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]any{"done": true, "reading_id": id, "status": status}))
}

// generate дописывает interpretation+status: AI при ключе, иначе fallback done.
// При ошибке AI: fallback-текст + pending_fallback (воркер допишет, см. ai/worker.go).
// S06: дедлайн 25с на всю генерацию.
func (s *Service) generate(ctx context.Context, id, spreadCode, question string, cards []cardDraw) {
	if s.gw == nil || !s.gw.Enabled() {
		_, _ = s.pg.Exec(ctx,
			`UPDATE readings SET interpretation=$1, status='done' WHERE id=$2`,
			s.fallbackText(ctx, spreadCode, cards), id)
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
		defer apierr.Recover() // S06
		text, _, _, err := s.gw.Stream(ctx, id, spreadName, positions, values, question, ch)
		done <- res{text, err}
	}()
	var sb strings.Builder
	for t := range ch {
		sb.WriteString(t)
	}
	r := <-done
	if r.err != nil {
		_, _ = s.pg.Exec(ctx,
			`UPDATE readings SET interpretation=$1, status='pending_fallback' WHERE id=$2`,
			s.fallbackText(ctx, spreadCode, cards)+"\n\nПолное толкование допишется автоматически.", id)
		return
	}
	if ai.ContainsStopWords(r.text) {
		_, _ = s.pg.Exec(ctx,
			`UPDATE readings SET interpretation=$1, status='filtered' WHERE id=$2`, ai.SafeReplacement, id)
		_, _ = s.pg.Exec(ctx, `
			INSERT INTO ai_logs (reading_id, model, prompt_hash, status, error)
			VALUES ($1,'filter','', 'filtered','stop-words')`, id)
		return
	}
	_, _ = s.pg.Exec(ctx,
		`UPDATE readings SET interpretation=$1, status='done' WHERE id=$2`, r.text, id)
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

// createFiltered сохраняет кризисное чтение без списания лимита (статус filtered).
// Возвращает id или "" (ошибка уже записана). Race закрыт ON CONFLICT + findByKey.
func (s *Service) createFiltered(ctx context.Context, w http.ResponseWriter, uid string, req createRequest, key string, n int) string {
	seed := time.Now().UnixNano()
	cards := draw(seed, n)
	cardsJSON, _ := json.Marshal(cards)
	var id string
	err := s.pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, interpretation, seed, algo_version, status, idempotency_key)
		VALUES ($1,$2,$3,$4,$5,$6,1,'filtered',$7)
		ON CONFLICT (user_id, idempotency_key) DO NOTHING
		RETURNING id`, uid, req.SpreadCode, req.Question, cardsJSON, crisisText, seed, key).Scan(&id)
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
		s.streamReading(w, r, id)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"reading_id": id})
}

// streamReading стримит сохраненный текст токенами-словами (T13: живые токены OpenRouter).
func (s *Service) streamReading(w http.ResponseWriter, r *http.Request, id string) {
	var text, status string
	if err := s.pg.QueryRow(r.Context(),
		`SELECT interpretation, status FROM readings WHERE id=$1`, id).Scan(&text, &status); err != nil {
		apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Расклад не найден")
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
		`SELECT code, price_rub, stars_amount FROM plans WHERE is_active AND code != 'free' ORDER BY price_rub`)
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
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	q := r.URL.Query().Get("q")
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
		SELECT id, spread_code, question, left(interpretation, 160), created_at
		  FROM readings WHERE user_id=$1 AND ($3='' OR question ILIKE '%'||$3||'%')
		 ORDER BY created_at DESC LIMIT $2 OFFSET $4`, uid, limit, q, offset)
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
	var owner string
	err := s.pg.QueryRow(ctx, `
		SELECT user_id, spread_code, question, cards, interpretation, status, created_at
		  FROM readings WHERE id=$1`, id).Scan(&owner, &spread, &question, &cards, &interp, &status, &created)
	if err != nil || owner != uid {
		apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Расклад не найден")
		return
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
