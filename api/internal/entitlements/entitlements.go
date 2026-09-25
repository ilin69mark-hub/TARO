// Package entitlements — единый чекер прав (см. docs/project-book/02-functional/05).
// Порядок: subscription(valid_until) → love_weekly → single → daily.
package entitlements

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
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

// mondayMSK возвращает дату понедельника текущей ISO-недели (для love_week, см. S03).
func mondayMSK(now time.Time) string {
	msk, _ := time.LoadLocation("Europe/Moscow")
	if msk == nil {
		msk = time.FixedZone("MSK", 3*3600)
	}
	local := now.In(msk)
	wd := int(local.Weekday())
	if wd == 0 {
		wd = 7
	}
	return local.AddDate(0, 0, -(wd - 1)).Format("2006-01-02")
}

// mondayMidnightMSK — unix следующего понедельника 00:00 MSK (TTL love-ключа, см. S03).
func mondayMidnightMSK(now time.Time) int64 {
	msk, _ := time.LoadLocation("Europe/Moscow")
	if msk == nil {
		msk = time.FixedZone("MSK", 3*3600)
	}
	local := now.In(msk)
	wd := int(local.Weekday())
	if wd == 0 {
		wd = 7
	}
	next := time.Date(local.Year(), local.Month(), local.Day()+(8-wd), 0, 0, 0, 0, msk)
	return next.Unix()
}

// mskDate — сегодняшняя дата МСК.
func mskDate(now time.Time) string {
	msk, _ := time.LoadLocation("Europe/Moscow")
	if msk == nil {
		msk = time.FixedZone("MSK", 3*3600)
	}
	return now.In(msk).Format("2006-01-02")
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

func (s *Service) consume(ctx context.Context, key string, limit int, expireAt int64) (bool, error) {
	if s.rd == nil {
		return false, errors.New("redis unavailable")
	}
	n, err := s.rd.Eval(ctx, consumeLua, []string{key}, limit, expireAt).Int()
	if err != nil {
		return false, err
	}
	return n != -1, nil
}

func (s *Service) pgConsumeValue(ctx context.Context, userID, kind string, limit int, period string) (bool, int, error) {
	if limit <= 0 {
		return false, 0, nil
	}
	if s.pg == nil {
		return false, 0, errors.New("postgres unavailable")
	}
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var used int
	var currentPeriod *string
	var query string
	if kind == "daily" {
		query = `
			INSERT INTO entitlements (user_id)
			VALUES ($1)
			ON CONFLICT (user_id) DO UPDATE SET user_id=EXCLUDED.user_id
			RETURNING free_used_today, free_date::text`
	} else {
		query = `
			INSERT INTO entitlements (user_id)
			VALUES ($1)
			ON CONFLICT (user_id) DO UPDATE SET user_id=EXCLUDED.user_id
			RETURNING love_used_week, love_week::text`
	}
	if err := tx.QueryRow(ctx, query, userID).Scan(&used, &currentPeriod); err != nil {
		return false, 0, err
	}
	value := used + 1
	if currentPeriod == nil || *currentPeriod != period {
		value = 1
	}
	if value > limit {
		return false, used, nil
	}
	if kind == "daily" {
		_, err = tx.Exec(ctx, `
			UPDATE entitlements
			   SET free_used_today=$2, free_date=$3::date
			 WHERE user_id=$1`, userID, value, period)
	} else {
		_, err = tx.Exec(ctx, `
			UPDATE entitlements
			   SET love_used_week=$2, love_week=$3::date
			 WHERE user_id=$1`, userID, value, period)
	}
	if err != nil {
		return false, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, 0, err
	}
	return true, value, nil
}

func (s *Service) pgConsume(ctx context.Context, userID, kind string, limit int, period string) (bool, error) {
	ok, _, err := s.pgConsumeValue(ctx, userID, kind, limit, period)
	return ok, err
}

func (s *Service) cacheQuota(ctx context.Context, key string, value int, expireAt int64) {
	if s.rd == nil {
		return
	}
	ttl := time.Until(time.Unix(expireAt, 0))
	if ttl <= 0 {
		ttl = time.Second
	}
	_ = s.rd.Set(ctx, key, value, ttl).Err()
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
	if isLove {
		weekly := atoi(s.config(ctx, "love.free_weekly", "1"), "1")
		ok, used, err := s.pgConsumeValue(ctx, userID, "love", weekly, mondayMSK(now))
		if err != nil {
			return Verdict{}, err
		}
		if ok {
			s.cacheQuota(ctx, "ent:"+userID+":love:"+weekKey(now), used, mondayMidnightMSK(now))
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
	daily := atoi(s.config(ctx, "free.daily_limit", "1"), "1")
	ok, used, err := s.pgConsumeValue(ctx, userID, "daily", daily, mskDate(now))
	if err != nil {
		return Verdict{}, err
	}
	if ok {
		s.cacheQuota(ctx, "ent:"+userID+":"+mskDate(now), used, midnightMSK(now))
		return Verdict{Allow: true, Reason: "daily"}, nil
	}
	return Verdict{Reason: "limit_exceeded"}, nil
}

var errReadingAuthorizationInvalid = errors.New("reading authorization state is invalid")

const recoveryAuthorizationTimeout = 5 * time.Second

type authorizationReceipt struct {
	Kind          string
	EntitlementID string
	PeriodStart   *time.Time
}

func (s *Service) AuthorizeReading(ctx context.Context, readingID, userID, spreadCode string) (Verdict, error) {
	if s == nil || s.pg == nil || readingID == "" || userID == "" {
		return Verdict{}, errReadingAuthorizationInvalid
	}
	dailyLimit := atoi(s.config(ctx, "free.daily_limit", "1"), "1")
	weeklyLimit := atoi(s.config(ctx, "love.free_weekly", "1"), "1")
	now := time.Now()
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return Verdict{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var lockedUser string
	if err := tx.QueryRow(ctx, `
		SELECT id::text FROM users WHERE id=$1 FOR KEY SHARE`, userID).Scan(&lockedUser); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Verdict{}, errReadingAuthorizationInvalid
		}
		return Verdict{}, err
	}
	var actualUser, actualSpread, status, quotaState string
	if err := tx.QueryRow(ctx, `
		SELECT user_id::text, spread_code, status, quota_state
		  FROM readings WHERE id=$1 AND user_id=$2 FOR UPDATE`, readingID, userID).
		Scan(&actualUser, &actualSpread, &status, &quotaState); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Verdict{}, errReadingAuthorizationInvalid
		}
		return Verdict{}, err
	}
	if actualUser != userID || actualSpread != spreadCode {
		return Verdict{}, errReadingAuthorizationInvalid
	}

	receipt, receiptErr := loadAuthorizationReceiptTx(ctx, tx, readingID)
	if receiptErr == nil {
		verdict, err := s.receiptVerdictTx(ctx, tx, readingID, receipt)
		if err != nil {
			return Verdict{}, err
		}
		if !verdict.Allow {
			if err := tx.Commit(ctx); err != nil {
				return Verdict{}, err
			}
			return verdict, nil
		}
		if quotaState != "allowed" {
			if status != "pending" && status != "pending_fallback" {
				return Verdict{}, errReadingAuthorizationInvalid
			}
			if err := setReadingQuotaAllowedTx(ctx, tx, readingID); err != nil {
				return Verdict{}, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return Verdict{}, err
		}
		return verdict, nil
	}
	if !errors.Is(receiptErr, pgx.ErrNoRows) {
		return Verdict{}, receiptErr
	}

	if quotaState == "allowed" {
		var linkedID string
		linkedErr := tx.QueryRow(ctx, `
			SELECT id::text
			  FROM single_entitlements
			 WHERE user_id=$1
			   AND consumed_reading_id=$2
			   AND (spread_code=$3 OR spread_code='any')
			 FOR UPDATE`, userID, readingID, spreadCode).Scan(&linkedID)
		kind := "legacy"
		entitlementID := ""
		verdict := Verdict{Allow: true, Reason: "subscription"}
		if linkedErr == nil {
			kind = "single"
			entitlementID = linkedID
			verdict = Verdict{Allow: true, Reason: "single", SingleID: linkedID}
		} else if !errors.Is(linkedErr, pgx.ErrNoRows) {
			return Verdict{}, linkedErr
		}
		if _, err := insertAuthorizationReceiptTx(ctx, tx, readingID, userID, kind, entitlementID, nil); err != nil {
			return Verdict{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Verdict{}, err
		}
		return verdict, nil
	}
	if quotaState != "unchecked" || (status != "pending" && status != "pending_fallback") {
		if status == "cancelled" && quotaState == "denied" {
			return Verdict{Reason: "limit_exceeded"}, nil
		}
		return Verdict{}, errReadingAuthorizationInvalid
	}

	var active bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM subscriptions
			 WHERE user_id=$1 AND status='active' AND valid_until > now()
		)`, userID).Scan(&active); err != nil {
		return Verdict{}, err
	}
	var isPremium, isLove bool
	if err := tx.QueryRow(ctx, `
		SELECT is_premium, code='love' FROM spreads WHERE code=$1 AND is_active`, spreadCode).
		Scan(&isPremium, &isLove); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Verdict{}, errReadingAuthorizationInvalid
		}
		return Verdict{}, err
	}

	if isPremium {
		var linkedID string
		linkedErr := tx.QueryRow(ctx, `
			SELECT id::text
			  FROM single_entitlements
			 WHERE user_id=$1
			   AND consumed_reading_id=$2
			   AND (spread_code=$3 OR spread_code='any')
			 FOR UPDATE`, userID, readingID, spreadCode).Scan(&linkedID)
		if linkedErr == nil {
			if _, err := insertAuthorizationReceiptTx(ctx, tx, readingID, userID, "single", linkedID, nil); err != nil {
				return Verdict{}, err
			}
			if err := setReadingQuotaAllowedTx(ctx, tx, readingID); err != nil {
				return Verdict{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return Verdict{}, err
			}
			return Verdict{Allow: true, Reason: "single", SingleID: linkedID}, nil
		}
		if !errors.Is(linkedErr, pgx.ErrNoRows) {
			return Verdict{}, linkedErr
		}
	}

	verdict := Verdict{Allow: true}
	var period *time.Time
	if active {
		verdict.Reason = "subscription"
	} else if isLove {
		ok, _, consumeErr := consumeReadingQuotaTx(ctx, tx, userID, "love", weeklyLimit, mondayMSK(now))
		if consumeErr != nil {
			return Verdict{}, consumeErr
		}
		if !ok {
			if err := setReadingQuotaDeniedTx(ctx, tx, readingID); err != nil {
				return Verdict{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return Verdict{}, err
			}
			return Verdict{Reason: "limit_exceeded"}, nil
		}
		periodValue, _ := time.Parse("2006-01-02", mondayMSK(now))
		period = &periodValue
		verdict.Reason = "love_weekly"
	} else if isPremium {
		singleID, selectErr := lockSingleEntitlementTx(ctx, tx, userID, spreadCode)
		err := selectErr
		if errors.Is(err, pgx.ErrNoRows) {
			if err := setReadingQuotaDeniedTx(ctx, tx, readingID); err != nil {
				return Verdict{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return Verdict{}, err
			}
			return Verdict{Reason: "limit_exceeded"}, nil
		}
		if err != nil {
			return Verdict{}, err
		}
		var linked string
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(consumed_reading_id::text,'')
			  FROM single_entitlements WHERE id=$1 FOR UPDATE`, singleID).Scan(&linked); err != nil {
			return Verdict{}, err
		}
		if linked != "" {
			return Verdict{}, errReadingAuthorizationInvalid
		}
		tag, err := tx.Exec(ctx, `
			UPDATE single_entitlements
			   SET consumed_reading_id=$1
			 WHERE id=$2 AND consumed_reading_id IS NULL`, readingID, singleID)
		if err != nil {
			return Verdict{}, err
		}
		if tag.RowsAffected() != 1 {
			return Verdict{}, errReadingAuthorizationInvalid
		}
		verdict.Reason = "single"
		verdict.SingleID = singleID
	} else {
		ok, _, consumeErr := consumeReadingQuotaTx(ctx, tx, userID, "daily", dailyLimit, mskDate(now))
		if consumeErr != nil {
			return Verdict{}, consumeErr
		}
		if !ok {
			if err := setReadingQuotaDeniedTx(ctx, tx, readingID); err != nil {
				return Verdict{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return Verdict{}, err
			}
			return Verdict{Reason: "limit_exceeded"}, nil
		}
		periodValue, _ := time.Parse("2006-01-02", mskDate(now))
		period = &periodValue
		verdict.Reason = "daily"
	}

	if _, err := insertAuthorizationReceiptTx(ctx, tx, readingID, userID, verdict.Reason, verdict.SingleID, period); err != nil {
		return Verdict{}, err
	}
	if err := setReadingQuotaAllowedTx(ctx, tx, readingID); err != nil {
		return Verdict{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Verdict{}, err
	}
	return verdict, nil
}

func lockSingleEntitlementTx(ctx context.Context, tx pgx.Tx, userID, spreadCode string) (string, error) {
	var singleID string
	err := tx.QueryRow(ctx, `
		SELECT id::text
		  FROM single_entitlements
		 WHERE user_id=$1
		   AND (spread_code=$2 OR spread_code='any')
		   AND consumed_reading_id IS NULL
		 ORDER BY created_at, id
		 LIMIT 1
		 FOR UPDATE SKIP LOCKED`, userID, spreadCode).Scan(&singleID)
	if !errors.Is(err, pgx.ErrNoRows) {
		return singleID, err
	}
	err = tx.QueryRow(ctx, `
		SELECT id::text
		  FROM single_entitlements
		 WHERE user_id=$1
		   AND (spread_code=$2 OR spread_code='any')
		   AND consumed_reading_id IS NULL
		 ORDER BY created_at, id
		 LIMIT 1
		 FOR UPDATE`, userID, spreadCode).Scan(&singleID)
	return singleID, err
}

func (s *Service) receiptVerdictTx(ctx context.Context, tx pgx.Tx, readingID string, receipt authorizationReceipt) (Verdict, error) {
	switch receipt.Kind {
	case "subscription", "legacy":
		return Verdict{Allow: true, Reason: "subscription"}, nil
	case "daily":
		return Verdict{Allow: true, Reason: "daily"}, nil
	case "love_weekly":
		return Verdict{Allow: true, Reason: "love_weekly"}, nil
	case "single":
		if receipt.EntitlementID == "" {
			return Verdict{Reason: "limit_exceeded"}, nil
		}
		var linked string
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(consumed_reading_id::text,'')
			  FROM single_entitlements WHERE id=$1 FOR UPDATE`, receipt.EntitlementID).Scan(&linked); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Verdict{}, errReadingAuthorizationInvalid
			}
			return Verdict{}, err
		}
		if linked != "" && linked != readingID {
			return Verdict{}, errReadingAuthorizationInvalid
		}
		if linked == "" {
			tag, err := tx.Exec(ctx, `
				UPDATE single_entitlements
				   SET consumed_reading_id=$1
				 WHERE id=$2 AND consumed_reading_id IS NULL`, readingID, receipt.EntitlementID)
			if err != nil {
				return Verdict{}, err
			}
			if tag.RowsAffected() != 1 {
				return Verdict{}, errReadingAuthorizationInvalid
			}
		}
		return Verdict{Allow: true, Reason: "single", SingleID: receipt.EntitlementID}, nil
	default:
		return Verdict{}, errReadingAuthorizationInvalid
	}
}

func loadAuthorizationReceiptTx(ctx context.Context, tx pgx.Tx, readingID string) (authorizationReceipt, error) {
	var receipt authorizationReceipt
	var period *time.Time
	err := tx.QueryRow(ctx, `
		SELECT kind, COALESCE(entitlement_id::text,''), period_start
		  FROM reading_authorization_receipts
		 WHERE reading_id=$1
		 FOR UPDATE`, readingID).Scan(&receipt.Kind, &receipt.EntitlementID, &period)
	receipt.PeriodStart = period
	return receipt, err
}

func insertAuthorizationReceiptTx(ctx context.Context, tx pgx.Tx, readingID, userID, kind, entitlementID string, period *time.Time) (authorizationReceipt, error) {
	var entitlement any
	if entitlementID != "" {
		entitlement = entitlementID
	}
	var periodValue any
	if period != nil {
		periodValue = *period
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO reading_authorization_receipts
			(reading_id, user_id, kind, entitlement_id, period_start)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (reading_id) DO NOTHING`, readingID, userID, kind, entitlement, periodValue)
	if err != nil {
		return authorizationReceipt{}, err
	}
	if tag.RowsAffected() == 1 {
		return authorizationReceipt{Kind: kind, EntitlementID: entitlementID, PeriodStart: period}, nil
	}
	existing, err := loadAuthorizationReceiptTx(ctx, tx, readingID)
	if err != nil {
		return authorizationReceipt{}, err
	}
	if existing.Kind != kind || existing.EntitlementID != entitlementID {
		return authorizationReceipt{}, errReadingAuthorizationInvalid
	}
	return existing, nil
}

func setReadingQuotaAllowedTx(ctx context.Context, tx pgx.Tx, readingID string) error {
	tag, err := tx.Exec(ctx, `
		UPDATE readings
		   SET quota_state='allowed', worker_claim_token=NULL,
		       worker_lease_until=now()+interval '2 minutes', worker_attempts=0, updated_at=now()
		 WHERE id=$1 AND quota_state='unchecked'`, readingID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errReadingAuthorizationInvalid
	}
	return nil
}

func setReadingQuotaDeniedTx(ctx context.Context, tx pgx.Tx, readingID string) error {
	tag, err := tx.Exec(ctx, `
		UPDATE readings
		   SET status='cancelled', quota_state='denied', worker_claim_token=NULL,
		       worker_lease_until=NULL, updated_at=now()
		 WHERE id=$1 AND quota_state='unchecked'
		   AND status IN ('pending', 'pending_fallback')`, readingID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errReadingAuthorizationInvalid
	}
	return nil
}

func consumeReadingQuotaTx(ctx context.Context, tx pgx.Tx, userID, kind string, limit int, period string) (bool, int, error) {
	if limit <= 0 {
		return false, 0, nil
	}
	var used int
	var currentPeriod *string
	var query string
	if kind == "daily" {
		query = `
			INSERT INTO entitlements (user_id)
			VALUES ($1)
			ON CONFLICT (user_id) DO UPDATE SET user_id=EXCLUDED.user_id
			RETURNING free_used_today, free_date::text`
	} else {
		query = `
			INSERT INTO entitlements (user_id)
			VALUES ($1)
			ON CONFLICT (user_id) DO UPDATE SET user_id=EXCLUDED.user_id
			RETURNING love_used_week, love_week::text`
	}
	if err := tx.QueryRow(ctx, query, userID).Scan(&used, &currentPeriod); err != nil {
		return false, 0, err
	}
	value := used + 1
	if currentPeriod == nil || *currentPeriod != period {
		value = 1
	}
	if value > limit {
		return false, used, nil
	}
	if kind == "daily" {
		_, err := tx.Exec(ctx, `
			UPDATE entitlements
			   SET free_used_today=$2, free_date=$3::date
			 WHERE user_id=$1`, userID, value, period)
		if err != nil {
			return false, 0, err
		}
	} else {
		_, err := tx.Exec(ctx, `
			UPDATE entitlements
			   SET love_used_week=$2, love_week=$3::date
			 WHERE user_id=$1`, userID, value, period)
		if err != nil {
			return false, 0, err
		}
	}
	return true, value, nil
}

func (s *Service) RecoverPendingAuthorizations(ctx context.Context, limit int) error {
	if s == nil || s.pg == nil || limit <= 0 {
		return nil
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.pg.Query(ctx, `
		WITH candidates AS (
			SELECT id
			  FROM readings
			 WHERE status IN ('pending', 'pending_fallback')
			   AND quota_state='unchecked'
			   AND (worker_lease_until IS NULL OR worker_lease_until <= now())
			 ORDER BY updated_at, created_at
			 FOR UPDATE SKIP LOCKED
			 LIMIT $1
		)
		UPDATE readings r
		   SET worker_claim_token=gen_random_uuid(),
		       worker_lease_until=now()+interval '2 minutes'
		  FROM candidates
		 WHERE r.id=candidates.id
		RETURNING r.id::text, r.user_id::text, r.spread_code, r.worker_claim_token::text`, limit)
	if err != nil {
		return err
	}
	type candidate struct {
		id, userID, spreadCode, token string
	}
	jobs := make([]candidate, 0, limit)
	for rows.Next() {
		var job candidate
		if err := rows.Scan(&job.id, &job.userID, &job.spreadCode, &job.token); err == nil {
			jobs = append(jobs, job)
		}
	}
	rows.Close()
	for _, job := range jobs {
		authorizationCtx, cancel := context.WithTimeout(ctx, recoveryAuthorizationTimeout)
		_, authorizationErr := s.AuthorizeReading(authorizationCtx, job.id, job.userID, job.spreadCode)
		cancel()
		if authorizationErr != nil {
			if errors.Is(authorizationErr, errReadingAuthorizationInvalid) {
				_, _ = s.pg.Exec(ctx, `
					UPDATE readings
					   SET status='failed', quota_state='error', worker_claim_token=NULL,
					       worker_lease_until=NULL, updated_at=now()
					 WHERE id=$1 AND quota_state='unchecked' AND worker_claim_token=$2`, job.id, job.token)
				continue
			}
			_, _ = s.pg.Exec(ctx, `
				UPDATE readings
				   SET worker_claim_token=NULL, worker_lease_until=now()+interval '30 seconds', updated_at=now()
				 WHERE id=$1 AND quota_state='unchecked' AND worker_claim_token=$2`, job.id, job.token)
		}
	}
	return nil
}

// GrantBonusDays продлевает valid_until: max(now, valid)+days новой строкой bonus-плана.
// Используют рефералка (T14) и платежи синглов? Нет — синглы идут в single_entitlements.
// planCode обязан существовать в plans (trial_3d, referral_bonus — см. миграции).
func (s *Service) GrantBonusDays(ctx context.Context, userID, planCode string, days int) error {
	var planID string
	if err := s.pg.QueryRow(ctx,
		`SELECT id FROM (
			SELECT DISTINCT ON (code) id, code, is_active
			FROM plans WHERE code=$1
			ORDER BY code, valid_from DESC
		) latest WHERE latest.is_active`, planCode).Scan(&planID); err != nil {
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
		`SELECT code, price_rub, stars_amount, duration_days
		 FROM (
			SELECT DISTINCT ON (code) code, price_rub, stars_amount, duration_days, is_active
			FROM plans WHERE code IN ('month_299','year_2490','single_99')
			ORDER BY code, valid_from DESC
		 ) latest
		 WHERE latest.is_active`)
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
	var freeUsed, loveUsed int
	if err := s.pg.QueryRow(ctx, `
		SELECT
			COALESCE((SELECT free_used_today FROM entitlements WHERE user_id=$1 AND free_date=$2::date), 0),
			COALESCE((SELECT love_used_week FROM entitlements WHERE user_id=$1 AND love_week=$3::date), 0)`,
		uid, mskDate(now), mondayMSK(now)).Scan(&freeUsed, &loveUsed); err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Не удалось загрузить лимиты")
		return
	}
	s.cacheQuota(ctx, "ent:"+uid+":"+mskDate(now), freeUsed, midnightMSK(now))
	s.cacheQuota(ctx, "ent:"+uid+":love:"+weekKey(now), loveUsed, mondayMidnightMSK(now))
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
