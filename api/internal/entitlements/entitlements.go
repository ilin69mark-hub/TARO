// Package entitlements — единый чекер прав (см. docs/project-book/02-functional/05).
// Порядок: subscription(valid_until) → love_weekly → single → daily.
// Счетчики daily/love — только атомарный Lua (INCR+EXPIREAT+сравнение), иначе race.
// PG — источник правды; Redis запрещено дропать для ent:* (см. 05-cache-redis.md).
package entitlements

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"taro/api/internal/apierr"
	"taro/api/internal/auth"
)

// consumeLua: если счетчик >= лимита — вернуть -1 без инкремента, иначе INCR (+EXPIREAT на первом).
// KEYS[1]=key, ARGV[1]=limit, ARGV[2]=expireat unix.
const consumeLua = `
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
if cur >= tonumber(ARGV[1]) then return -1 end
local n = redis.call('INCR', KEYS[1])
if n == 1 then redis.call('EXPIREAT', KEYS[1], ARGV[2]) end
return n
`

// Service — чекер + выдача бонусов.
type Service struct {
	pg *pgxpool.Pool
	rd *redis.Client
}

// New возвращает сервис.
func New(pg *pgxpool.Pool, rd *redis.Client) *Service {
	return &Service{pg: pg, rd: rd}
}

// Verdict — результат проверки.
type Verdict struct {
	Allow    bool
	Reason   string // subscription | love_weekly | single | daily | limit_exceeded
	SingleID string // для reason=single: id single_entitlements (гасится при создании reading, T12)
}

// midnightMSK возвращает unix следующей полуночи Europe/Moscow.
func midnightMSK(now time.Time) int64 {
	msk, _ := time.LoadLocation("Europe/Moscow")
	if msk == nil {
		msk = time.FixedZone("MSK", 3*3600)
	}
	local := now.In(msk)
	next := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, msk)
	return next.Unix()
}

// weekKey возвращает ключ ISO-недели МСК: 2026-W39.
func weekKey(now time.Time) string {
	msk, _ := time.LoadLocation("Europe/Moscow")
	if msk == nil {
		msk = time.FixedZone("MSK", 3*3600)
	}
	y, w := now.In(msk).ISOWeek()
	return strconv.Itoa(y) + "-W" + strconv.Itoa(w)
}

// config читает лимит из app_config (дефолт при отсутствии).
func (s *Service) config(ctx context.Context, key, def string) string {
	var v string
	if err := s.pg.QueryRow(ctx, `SELECT value #>> '{}' FROM app_config WHERE key=$1`, key).Scan(&v); err != nil {
		return def
	}
	if v == "" {
		return def
	}
	return v
}

func atoi(s, def string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		n, _ = strconv.Atoi(def)
	}
	return n
}

// consume выполняет Lua-потребление: true если влезли в лимит.
func (s *Service) consume(ctx context.Context, key string, limit int, expireAt int64) (bool, error) {
	n, err := s.rd.Eval(ctx, consumeLua, []string{key}, limit, expireAt).Int()
	if err != nil {
		return false, err
	}
	return n != -1, nil
}

// Check проверяет право на расклад (порядок frozen, см. 02-functional/05).
func (s *Service) Check(ctx context.Context, userID, spreadCode string) (Verdict, error) {
	// 1. активная подписка (оплата, trial, referral-бонусы — все здесь)
	var active bool
	if err := s.pg.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM subscriptions WHERE user_id=$1 AND status='active' AND valid_until > now())`,
		userID).Scan(&active); err != nil {
		return Verdict{}, err
	}
	if active {
		return Verdict{Allow: true, Reason: "subscription"}, nil
	}
	// флаги спреда
	var isPremium bool
	var isLove bool
	if err := s.pg.QueryRow(ctx,
		`SELECT is_premium, code='love' FROM spreads WHERE code=$1 AND is_active`, spreadCode).Scan(&isPremium, &isLove); err != nil {
		return Verdict{}, err
	}
	now := time.Now()
	// 2. love для free — недельный счетчик
	if isLove {
		weekly := atoi(s.config(ctx, "love.free_weekly", "1"), "1")
		ok, err := s.consume(ctx, "ent:"+userID+":love:"+weekKey(now), weekly, midnightMSK(now)+6*24*3600)
		if err != nil {
			return Verdict{}, err
		}
		if ok {
			return Verdict{Allow: true, Reason: "love_weekly"}, nil
		}
		return Verdict{Reason: "limit_exceeded"}, nil
	}
	// 3. premium + разовая покупка (точный спред или 'any' — см. миграцию 006, T29)
	if isPremium {
		var singleID string
		err := s.pg.QueryRow(ctx,
			`SELECT id FROM single_entitlements
			  WHERE user_id=$1 AND (spread_code=$2 OR spread_code='any') AND consumed_reading_id IS NULL LIMIT 1`,
			userID, spreadCode).Scan(&singleID)
		if err == nil {
			return Verdict{Allow: true, Reason: "single", SingleID: singleID}, nil
		}
		return Verdict{Reason: "limit_exceeded"}, nil
	}
	// 4. обычный free — дневной счетчик
	daily := atoi(s.config(ctx, "free.daily_limit", "1"), "1")
	ok, err := s.consume(ctx, "ent:"+userID+":"+now.Format("2006-01-02"), daily, midnightMSK(now))
	if err != nil {
		return Verdict{}, err
	}
	if ok {
		return Verdict{Allow: true, Reason: "daily"}, nil
	}
	return Verdict{Reason: "limit_exceeded"}, nil
}

// GrantBonusDays продлевает valid_until: max(now, valid)+days новой строкой bonus-плана.
// Используют рефералка (T14) и платежи синглов? Нет — синглы идут в single_entitlements.
// planCode обязан существовать в plans (trial_3d, referral_bonus — см. миграции).
func (s *Service) GrantBonusDays(ctx context.Context, userID, planCode string, days int) error {
	var planID string
	if err := s.pg.QueryRow(ctx,
		`SELECT id FROM plans WHERE code=$1 AND is_active ORDER BY valid_from DESC LIMIT 1`, planCode).Scan(&planID); err != nil {
		return err
	}
	_, err := s.pg.Exec(ctx,
		`INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, valid_until)
		 SELECT $1,$2,$3,0, GREATEST(COALESCE(MAX(valid_until), now()), now()) + make_interval(days => $4)
		   FROM subscriptions WHERE user_id=$1 AND status='active'`,
		userID, planID, planCode, days)
	return err
}

// Plan — публичный тариф (цены не секрет, см. 02-functional/05).
type Plan struct {
	Code     string `json:"code"`
	Price    int    `json:"price_rub"`
	Stars    int    `json:"stars_amount"`
	Duration *int   `json:"duration_days"`
}

// HandlePlans — GET /v1/plans: только покупаемые тарифы (без free/trial/bonus).
func (s *Service) HandlePlans(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pg.Query(r.Context(),
		`SELECT DISTINCT ON (code) code, price_rub, stars_amount, duration_days FROM plans
		  WHERE is_active AND code IN ('month_299','year_2490','single_99')
		  ORDER BY code, valid_from DESC`)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось загрузить тарифы")
		return
	}
	defer rows.Close()
	out := []Plan{}
	for rows.Next() {
		var p Plan
		if err := rows.Scan(&p.Code, &p.Price, &p.Stars, &p.Duration); err != nil {
			continue
		}
		out = append(out, p)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// Me — GET /v1/entitlements/me: план, остатки, valid_until (+winback U28).
func (s *Service) HandleMe(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	ctx := r.Context()
	var validUntil *time.Time
	_ = s.pg.QueryRow(ctx,
		`SELECT MAX(valid_until) FROM subscriptions WHERE user_id=$1 AND status='active' AND valid_until > now()`,
		uid).Scan(&validUntil)
	// winback U28: была активная подписка, кончилась ≥14 дней назад, сейчас free.
	// NB: pgx требует $1/$2 явно (повтор $1 + 2 аргумента = ошибка, см. V16).
	var winback bool
	_ = s.pg.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM subscriptions WHERE user_id=$1 AND status IN ('active','expired')
		  AND valid_until < now() - interval '14 days')
		   AND NOT EXISTS(SELECT 1 FROM subscriptions WHERE user_id=$2 AND status='active' AND valid_until > now())`,
		uid, uid).Scan(&winback)
	daily := atoi(s.config(ctx, "free.daily_limit", "1"), "1")
	weekly := atoi(s.config(ctx, "love.free_weekly", "1"), "1")
	now := time.Now()
	freeUsed, _ := s.rd.Get(ctx, "ent:"+uid+":"+now.Format("2006-01-02")).Int()
	loveUsed, _ := s.rd.Get(ctx, "ent:"+uid+":love:"+weekKey(now)).Int()
	freeLeft := daily - freeUsed
	if freeLeft < 0 {
		freeLeft = 0
	}
	loveLeft := weekly - loveUsed
	if loveLeft < 0 {
		loveLeft = 0
	}
	plan := "free"
	if validUntil != nil {
		plan = "premium"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"plan":             plan,
		"free_left":        freeLeft,
		"love_left_week":   loveLeft,
		"valid_until":      validUntil,
		"winback_eligible": winback,
	})
}
