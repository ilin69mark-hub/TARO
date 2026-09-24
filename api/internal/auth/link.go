// Package auth — Telegram ID + UUID anon (см. docs/project-book/02-functional/02).
// JWT cookie taro_jwt: HttpOnly; Secure; SameSite=None + CSRF-header; Path=/; 30д.
// Trial 3д выдается 1 раз на связку tg_id+fingerprint (см. T09, 02-functional/05).
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"taro/api/internal/apierr"
)

type userCtxKey struct{}

type mergeUser struct {
	ID             string
	TGID           *int64
	AnonUUID       *string
	Fingerprint    string
	Role           string
	AgeConfirmedAt *time.Time
	ReferralCode   *string
	CreatedAt      time.Time
	Status         string
}

func (s *Service) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err != nil {
			ExpireAuthCookies(w)
			apierr.Write(w, http.StatusUnauthorized, apierr.CodeUnauthorized, "Нужен вход")
			return
		}
		claims, err := parseJWT(c.Value)
		if err != nil || claims.Subject == "" || claims.JTI == "" || !isUUID(claims.Subject) || strings.HasPrefix(claims.Subject, "admin:") {
			ExpireAuthCookies(w)
			apierr.Write(w, http.StatusUnauthorized, apierr.CodeUnauthorized, "Сессия истекла, войди снова")
			return
		}
		markerOK, err := s.sessionMarkerOK(r.Context(), claims)
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
			return
		}
		if !markerOK {
			ExpireAuthCookies(w)
			apierr.Write(w, http.StatusUnauthorized, apierr.CodeUnauthorized, "Сессия завершена, войди снова")
			return
		}
		var active bool
		if err := s.pg.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND status='active')`, claims.Subject).Scan(&active); err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
			return
		}
		if !active {
			ExpireAuthCookies(w)
			apierr.Write(w, http.StatusUnauthorized, apierr.CodeUnauthorized, "Сессия завершена, войди снова")
			return
		}
		ctx := context.WithValue(r.Context(), userCtxKey{}, claims.Subject)
		ctx = context.WithValue(ctx, sessionClaimsKey{}, claims)
		if r.Method == http.MethodDelete && r.URL.Path == "/v1/me" {
			ExpireAuthCookies(w)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func UserID(ctx context.Context) string {
	uid, _ := ctx.Value(userCtxKey{}).(string)
	return uid
}

func (s *Service) HandleLink(w http.ResponseWriter, r *http.Request) {
	current := UserID(r.Context())
	if current == "" {
		apierr.Write(w, http.StatusUnauthorized, apierr.CodeUnauthorized, "Нужен вход")
		return
	}
	var req struct {
		InitData    string `json:"initData"`
		Fingerprint string `json:"fingerprint"`
	}
	if !apierr.Decode(w, r, &req) || req.InitData == "" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректный initData")
		return
	}
	fp := fpOf(r, req.Fingerprint)
	if !validFingerprint(fp, true) {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нужен fingerprint")
		return
	}
	tgID, err := VerifyInitData(req.InitData, os.Getenv("TG_BOT_TOKEN"))
	if err != nil {
		apierr.Write(w, http.StatusUnauthorized, apierr.CodeInvalidTg, "Не удалось подтвердить Telegram")
		return
	}
	survivor, merged, err := s.Link(r.Context(), current, tgID, fp)
	if err != nil {
		switch {
		case errors.Is(err, errAlreadyLinked):
			apierr.Write(w, http.StatusConflict, "ALREADY_LINKED", "Telegram уже привязан")
		case err.Error() == "fp_mismatch":
			apierr.Write(w, http.StatusForbidden, "FP_MISMATCH", "Устройство не узнано, войди через Telegram")
		case errors.Is(err, errMergeSelf), errors.Is(err, errMergeCycle):
			apierr.Write(w, http.StatusConflict, "MERGE_CONFLICT", "Нельзя объединить эти аккаунты")
		default:
			apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось привязать Telegram")
		}
		return
	}
	tok, err := s.issueSession(r.Context(), survivor, UserTTL)
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
		return
	}
	writeCookie(w, tok, UserTTL)
	writeFpCookie(w, fp)
	csrf := s.issueCSRF(w, r.Context(), survivor)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"merged": merged, "user_id": survivor, "csrf_token": csrf})
}

var (
	errAlreadyLinked = errors.New("already_linked")
	errMergeSelf     = errors.New("merge_self")
	errMergeCycle    = errors.New("merge_cycle")
)

func (s *Service) Link(ctx context.Context, current string, tgID int64, fingerprint string) (string, bool, error) {
	if !validFingerprint(fingerprint, true) {
		return "", false, fmt.Errorf("fingerprint_required")
	}
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, tgID); err != nil {
		return "", false, err
	}
	cur, err := loadUserForUpdate(ctx, tx, current)
	if err != nil {
		return "", false, err
	}
	if cur.Status != "active" {
		return "", false, fmt.Errorf("user inactive")
	}
	if cur.Fingerprint != "" && !fingerprintsEqual(cur.Fingerprint, fingerprint) {
		return "", false, fmt.Errorf("fp_mismatch")
	}
	if cur.TGID != nil {
		if *cur.TGID == tgID {
			if cur.Fingerprint != "" && !fingerprintsEqual(cur.Fingerprint, fingerprint) {
				return "", false, fmt.Errorf("fp_mismatch")
			}
			if cur.Fingerprint == "" {
				if _, err := tx.Exec(ctx, `UPDATE users SET fingerprint=$1 WHERE id=$2`, fingerprint, current); err != nil {
					return "", false, err
				}
			}
			if err := tx.Commit(ctx); err != nil {
				return "", false, err
			}
			return current, true, nil
		}
		return "", false, errAlreadyLinked
	}
	var other mergeUser
	err = loadUserByTG(ctx, tx, tgID, &other)
	if err == pgx.ErrNoRows {
		tag, err := tx.Exec(ctx, `UPDATE users SET tg_id=$1 WHERE id=$2 AND tg_id IS NULL AND status='active'`, tgID, current)
		if err != nil {
			return "", false, err
		}
		if tag.RowsAffected() != 1 {
			return "", false, errAlreadyLinked
		}
		if _, _, err := grantTrialTx(ctx, tx, current, tgID, fingerprint); err != nil {
			return "", false, err
		}
		if err := s.revokeSessions(ctx, current); err != nil {
			return "", false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", false, err
		}
		return current, true, nil
	}
	if err != nil {
		return "", false, err
	}
	if other.ID == current {
		return "", false, errMergeSelf
	}
	if other.Status != "active" {
		return "", false, fmt.Errorf("user inactive")
	}
	if other.Fingerprint != "" && !fingerprintsEqual(other.Fingerprint, fingerprint) {
		return "", false, fmt.Errorf("fp_mismatch")
	}
	if err := rejectReferralCyclesTx(ctx, tx, other.ID, current); err != nil {
		return "", false, err
	}
	if err := rejectReferralSelfTx(ctx, tx, other.ID, current); err != nil {
		return "", false, err
	}
	if err := mergeMetadataTx(ctx, tx, other, cur, fingerprint); err != nil {
		return "", false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE readings l SET idempotency_key=l.id::text
		WHERE l.user_id=$2 AND l.idempotency_key IS NOT NULL
		  AND EXISTS (SELECT 1 FROM readings k WHERE k.user_id=$1 AND k.idempotency_key=l.idempotency_key AND k.id<>l.id)`, other.ID, current); err != nil {
		return "", false, err
	}
	for _, q := range []string{
		`UPDATE readings SET user_id=$1 WHERE user_id=$2`,
		`UPDATE subscriptions SET user_id=$1 WHERE user_id=$2`,
		`UPDATE payments SET user_id=$1 WHERE user_id=$2`,
		`UPDATE single_entitlements SET user_id=$1 WHERE user_id=$2`,
		`UPDATE referrals SET referrer_id=$1 WHERE referrer_id=$2`,
		`UPDATE diary_entries SET user_id=$1 WHERE user_id=$2`,
		`UPDATE push_subscriptions SET user_id=$1 WHERE user_id=$2`,
		`UPDATE push_preferences SET user_id=$1 WHERE user_id=$2 AND NOT EXISTS (SELECT 1 FROM push_preferences WHERE user_id=$1)`,
		`DELETE FROM push_preferences WHERE user_id=$2 AND EXISTS (SELECT 1 FROM push_preferences WHERE user_id=$1)`,
		`UPDATE admin_audit SET admin_id=$1 WHERE admin_id=$2`,
		`UPDATE trial_grants SET user_id=$1 WHERE user_id=$2`,
	} {
		if _, err := tx.Exec(ctx, q, other.ID, current); err != nil {
			return "", false, err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM subscriptions s USING subscriptions keep
		WHERE s.user_id=$1 AND keep.user_id=$1 AND s.id<>keep.id
		  AND s.plan_code=keep.plan_code AND s.valid_until <= keep.valid_until`, other.ID); err != nil {
		return "", false, err
	}
	if _, _, err := grantTrialTx(ctx, tx, other.ID, tgID, fingerprint); err != nil {
		return "", false, err
	}
	if err := resolveReferralMergeTx(ctx, tx, other.ID, current); err != nil {
		return "", false, err
	}
	if err := mergeCountersTx(ctx, tx, other.ID, current); err != nil {
		return "", false, err
	}
	if err := s.mergeRedisQuota(ctx, other.ID, current); err != nil {
		return "", false, err
	}
	if err := s.ensureRedisQuotaFloor(ctx, tx, other.ID); err != nil {
		return "", false, err
	}
	if err := s.revokeSessions(ctx, current, other.ID); err != nil {
		return "", false, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id=$1`, current); err != nil {
		return "", false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", false, err
	}
	return other.ID, true, nil
}

func loadUserForUpdate(ctx context.Context, tx pgx.Tx, id string) (mergeUser, error) {
	return scanUser(tx.QueryRow(ctx, `SELECT id::text, tg_id, anon_uuid::text, COALESCE(fingerprint,''), role,
		age_confirmed_at, referral_code, created_at, status FROM users WHERE id=$1 FOR UPDATE`, id))
}

func loadUserByTG(ctx context.Context, tx pgx.Tx, tgID int64, dst *mergeUser) error {
	u, err := scanUser(tx.QueryRow(ctx, `SELECT id::text, tg_id, anon_uuid::text, COALESCE(fingerprint,''), role,
		age_confirmed_at, referral_code, created_at, status FROM users WHERE tg_id=$1 FOR UPDATE`, tgID))
	if err != nil {
		return err
	}
	*dst = u
	return nil
}

func scanUser(row pgx.Row) (mergeUser, error) {
	var u mergeUser
	err := row.Scan(&u.ID, &u.TGID, &u.AnonUUID, &u.Fingerprint, &u.Role, &u.AgeConfirmedAt, &u.ReferralCode, &u.CreatedAt, &u.Status)
	return u, err
}

func mergeMetadataTx(ctx context.Context, tx pgx.Tx, survivor, loser mergeUser, fingerprint string) error {
	if survivor.Fingerprint != "" && loser.Fingerprint != "" && !fingerprintsEqual(survivor.Fingerprint, loser.Fingerprint) {
		return fmt.Errorf("fp_mismatch")
	}
	if survivor.Fingerprint == "" {
		survivor.Fingerprint = loser.Fingerprint
	}
	if survivor.Fingerprint == "" {
		survivor.Fingerprint = fingerprint
	}
	if survivor.Role != "admin" && loser.Role == "admin" {
		survivor.Role = "admin"
	}
	if loser.AgeConfirmedAt != nil && (survivor.AgeConfirmedAt == nil || loser.AgeConfirmedAt.After(*survivor.AgeConfirmedAt)) {
		survivor.AgeConfirmedAt = loser.AgeConfirmedAt
	}
	if survivor.CreatedAt.IsZero() || (!loser.CreatedAt.IsZero() && loser.CreatedAt.Before(survivor.CreatedAt)) {
		survivor.CreatedAt = loser.CreatedAt
	}
	if survivor.AnonUUID == nil {
		survivor.AnonUUID = loser.AnonUUID
		if survivor.AnonUUID != nil {
			if _, err := tx.Exec(ctx, `UPDATE users SET anon_uuid=NULL WHERE id=$1`, loser.ID); err != nil {
				return err
			}
		}
	}
	if survivor.ReferralCode == nil {
		survivor.ReferralCode = loser.ReferralCode
		if survivor.ReferralCode != nil {
			if _, err := tx.Exec(ctx, `UPDATE users SET referral_code=NULL WHERE id=$1`, loser.ID); err != nil {
				return err
			}
		}
	}
	_, err := tx.Exec(ctx, `UPDATE users SET fingerprint=$1, role=$2, age_confirmed_at=$3, created_at=$4,
		anon_uuid=$5, referral_code=$6 WHERE id=$7`, survivor.Fingerprint, survivor.Role, survivor.AgeConfirmedAt,
		survivor.CreatedAt, survivor.AnonUUID, survivor.ReferralCode, survivor.ID)
	return err
}

func rejectReferralCyclesTx(ctx context.Context, tx pgx.Tx, ids ...string) error {
	for _, id := range ids {
		var cycle bool
		if err := tx.QueryRow(ctx, `WITH RECURSIVE walk(start, node) AS (
			SELECT $1::uuid, r.referee_id FROM referrals r WHERE r.referrer_id=$1
			UNION
			SELECT w.start, r.referee_id FROM walk w JOIN referrals r ON r.referrer_id=w.node
		) SELECT EXISTS(SELECT 1 FROM walk WHERE node=start)`, id).Scan(&cycle); err != nil {
			return err
		}
		if cycle {
			return errMergeCycle
		}
	}
	return nil
}

func rejectReferralSelfTx(ctx context.Context, tx pgx.Tx, ids ...string) error {
	for _, id := range ids {
		var self bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM referrals
			WHERE (referrer_id=$1 OR referee_id=$1) AND referrer_id=referee_id)`, id).Scan(&self); err != nil {
			return err
		}
		if self {
			return errMergeCycle
		}
	}
	return nil
}

func resolveReferralMergeTx(ctx context.Context, tx pgx.Tx, survivor, loser string) error {
	if err := rejectReferralSelfTx(ctx, tx, survivor, loser); err != nil {
		return err
	}
	var loserID, survivorID, loserStatus, survivorStatus string
	var loserBonus, survivorBonus int
	loserErr := tx.QueryRow(ctx, `SELECT id::text, status, bonus_days FROM referrals WHERE referee_id=$1 FOR UPDATE`, loser).Scan(&loserID, &loserStatus, &loserBonus)
	if loserErr == nil {
		survivorErr := tx.QueryRow(ctx, `SELECT id::text, status, bonus_days FROM referrals WHERE referee_id=$1 FOR UPDATE`, survivor).Scan(&survivorID, &survivorStatus, &survivorBonus)
		if survivorErr == nil {
			status := referralStatusMerge(survivorStatus, loserStatus)
			bonus := loserBonus
			if survivorBonus > bonus {
				bonus = survivorBonus
			}
			if _, err := tx.Exec(ctx, `UPDATE referrals SET status=$1, bonus_days=$2 WHERE id=$3`, status, bonus, survivorID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM referrals WHERE id=$1`, loserID); err != nil {
				return err
			}
		} else if survivorErr == pgx.ErrNoRows {
			if _, err := tx.Exec(ctx, `UPDATE referrals SET referee_id=$1 WHERE id=$2`, survivor, loserID); err != nil {
				return err
			}
		} else {
			return survivorErr
		}
	} else if loserErr != pgx.ErrNoRows {
		return loserErr
	}
	if _, err := tx.Exec(ctx, `UPDATE referrals SET referrer_id=$1 WHERE referrer_id=$2`, survivor, loser); err != nil {
		return err
	}
	var cycle bool
	if err := tx.QueryRow(ctx, `WITH RECURSIVE walk(start, node) AS (
		SELECT referee_id, referee_id FROM referrals WHERE referrer_id=$1
		UNION
		SELECT r.referee_id, w.node FROM referrals r JOIN walk w ON r.referrer_id=w.node
	) SELECT EXISTS(SELECT 1 FROM walk WHERE node=start)`, survivor).Scan(&cycle); err != nil {
		return err
	}
	if cycle {
		return errMergeCycle
	}
	return nil
}

func referralStatusMerge(a, b string) string {
	if a == "completed" || b == "completed" {
		return "completed"
	}
	if a == "pending" || b == "pending" {
		return "pending"
	}
	return "rejected"
}

func (s *Service) revokeSessions(ctx context.Context, userIDs ...string) error {
	keys := make([]string, 0, len(userIDs)*2)
	for _, id := range userIDs {
		keys = append(keys, sessKey(id), "csrf:"+id)
	}
	if len(keys) == 0 {
		return nil
	}
	return s.rd.Del(ctx, keys...).Err()
}

const mergeQuotaLua = `
local a = tonumber(redis.call('GET', KEYS[1]) or '0')
local b = tonumber(redis.call('GET', KEYS[2]) or '0')
local v = math.max(a, b)
if v <= 0 then return 0 end
local ttl1 = redis.call('PTTL', KEYS[1])
local ttl2 = redis.call('PTTL', KEYS[2])
local ttl = math.max(ttl1, ttl2)
if ttl <= 0 then ttl = 2592000000 end
redis.call('SET', KEYS[1], v, 'PX', ttl)
return v
`

func (s *Service) mergeRedisQuota(ctx context.Context, survivor, loser string) error {
	var cursor uint64
	for {
		keys, next, err := s.rd.Scan(ctx, cursor, "ent:"+loser+":*", 100).Result()
		if err != nil {
			return err
		}
		for _, loserKey := range keys {
			suffix := strings.TrimPrefix(loserKey, "ent:"+loser+":")
			if suffix == loserKey {
				continue
			}
			if _, err := s.rd.Eval(ctx, mergeQuotaLua, []string{"ent:" + survivor + ":" + suffix, loserKey}).Result(); err != nil {
				return err
			}
		}
		if len(keys) > 0 {
			if err := s.rd.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}

const quotaFloorLua = `
local current = tonumber(redis.call('GET', KEYS[1]) or '0')
local floor = tonumber(ARGV[1])
if current < floor then
  redis.call('SET', KEYS[1], floor, 'PX', ARGV[2])
end
return math.max(current, floor)
`

func (s *Service) ensureRedisQuotaFloor(ctx context.Context, tx pgx.Tx, userID string) error {
	var daily, love int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE((SELECT free_used_today FROM entitlements WHERE user_id=$1), 0),
		       COALESCE((SELECT love_used_week FROM entitlements WHERE user_id=$1), 0)`, userID).Scan(&daily, &love); err != nil {
		return err
	}
	now := time.Now()
	msk := time.FixedZone("MSK", 3*60*60)
	local := now.In(msk)
	dailyKey := "ent:" + userID + ":" + local.Format("2006-01-02")
	nextDay := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, msk)
	dailyTTL := time.Until(nextDay)
	if dailyTTL <= 0 {
		dailyTTL = time.Second
	}
	year, week := local.ISOWeek()
	loveKey := "ent:" + userID + ":love:" + strconv.Itoa(year) + "-W" + strconv.Itoa(week)
	daysUntilMonday := (8 - int(local.Weekday())) % 7
	if daysUntilMonday == 0 {
		daysUntilMonday = 7
	}
	nextMonday := time.Date(local.Year(), local.Month(), local.Day()+daysUntilMonday, 0, 0, 0, 0, msk)
	loveTTL := time.Until(nextMonday)
	if loveTTL <= 0 {
		loveTTL = time.Second
	}
	for _, item := range []struct {
		key   string
		value int
		ttl   time.Duration
	}{
		{dailyKey, daily, dailyTTL},
		{loveKey, love, loveTTL},
	} {
		if item.value <= 0 {
			continue
		}
		if _, err := s.rd.Eval(ctx, quotaFloorLua, []string{item.key}, item.value, item.ttl.Milliseconds()).Result(); err != nil {
			return err
		}
	}
	return nil
}

func mergeCountersTx(ctx context.Context, tx pgx.Tx, survivor, loser string) error {
	type counters struct {
		freeUsed int
		freeDate *string
		loveUsed int
		loveWeek *string
		refMonth int
		refKey   *string
		refLife  int
	}
	read := func(uid string) (counters, error) {
		var c counters
		err := tx.QueryRow(ctx,
			`SELECT free_used_today, free_date::text, love_used_week, love_week::text,
			        COALESCE(referral_bonus_month,0), referral_bonus_month_key,
			        COALESCE(referral_bonus_lifetime,0)
			   FROM entitlements WHERE user_id=$1 FOR UPDATE`, uid).Scan(&c.freeUsed, &c.freeDate, &c.loveUsed, &c.loveWeek, &c.refMonth, &c.refKey, &c.refLife)
		if err == pgx.ErrNoRows {
			return counters{}, nil
		}
		return c, err
	}
	a, err := read(survivor)
	if err != nil {
		return err
	}
	b, err := read(loser)
	if err != nil {
		return err
	}
	freeUsed := max(a.freeUsed, b.freeUsed)
	loveUsed := max(a.loveUsed, b.loveUsed)
	freeDate := a.freeDate
	if b.freeDate != nil && (freeDate == nil || *b.freeDate > *freeDate) {
		freeDate = b.freeDate
	}
	loveWeek := a.loveWeek
	if b.loveWeek != nil && (loveWeek == nil || *b.loveWeek > *loveWeek) {
		loveWeek = b.loveWeek
	}
	refMonth, refKey := a.refMonth, a.refKey
	if a.refKey == nil || (b.refKey != nil && (*b.refKey > *a.refKey || (*b.refKey == *a.refKey && b.refMonth > refMonth))) {
		refMonth, refKey = b.refMonth, b.refKey
	}
	refLife := max(a.refLife, b.refLife)
	_, err = tx.Exec(ctx, `
		INSERT INTO entitlements (user_id, free_used_today, free_date, love_used_week, love_week,
		  referral_bonus_month, referral_bonus_month_key, referral_bonus_lifetime)
		VALUES ($1,$2,$3::date,$4,$5::date,$6,$7,$8)
		ON CONFLICT (user_id) DO UPDATE SET
		  free_used_today = EXCLUDED.free_used_today,
		  free_date = EXCLUDED.free_date,
		  love_used_week = EXCLUDED.love_used_week,
		  love_week = EXCLUDED.love_week,
		  referral_bonus_month = EXCLUDED.referral_bonus_month,
		  referral_bonus_month_key = EXCLUDED.referral_bonus_month_key,
		  referral_bonus_lifetime = EXCLUDED.referral_bonus_lifetime`,
		survivor, freeUsed, freeDate, loveUsed, loveWeek, refMonth, refKey, refLife)
	return err
}
