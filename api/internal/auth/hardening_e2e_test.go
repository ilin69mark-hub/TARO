package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"taro/api/internal/testutil"
)

func liveAuth(t *testing.T) (context.Context, *pgxpool.Pool, *redis.Client, *Service) {
	t.Helper()
	ctx, pg, rd := testutil.Live(t)
	var status string
	if err := pg.QueryRow(ctx, `SELECT status FROM users LIMIT 1`).Scan(&status); err != nil {
		t.Skipf("auth status migration is not applied: %v", err)
	}
	return ctx, pg, rd, New(pg, rd)
}

func authCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestE2EExistingAnonRequiresFingerprintAndCookieWins(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-0123456789abcdef0123456789")
	ctx, pg, rd, svc := liveAuth(t)
	var uuid, legacyUUID string
	if err := pg.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&uuid); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&legacyUUID); err != nil {
		t.Fatal(err)
	}
	var uid string
	if err := pg.QueryRow(ctx, `INSERT INTO users (anon_uuid, fingerprint) VALUES ($1,'stored-fp') RETURNING id`, uuid).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	var legacyUID string
	if err := pg.QueryRow(ctx, `INSERT INTO users (anon_uuid, fingerprint) VALUES ($1,NULL) RETURNING id`, legacyUUID).Scan(&legacyUID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id IN ($1,$2)`, uid, legacyUID)
	})
	call := func(body, fpCookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/v1/auth/anon", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if fpCookie != "" {
			req.AddCookie(&http.Cookie{Name: FpCookie, Value: fpCookie})
		}
		rec := httptest.NewRecorder()
		svc.HandleAnon(rec, req)
		return rec
	}
	if rec := call(`{"uuid":"`+uuid+`"}`, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("missing fingerprint: %d", rec.Code)
	}
	if rec := call(`{"uuid":"`+uuid+`","fingerprint":"wrong"}`, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong fingerprint: %d", rec.Code)
	}
	rec := call(`{"uuid":"`+uuid+`","fingerprint":"wrong"}`, "stored-fp")
	if rec.Code != http.StatusOK {
		t.Fatalf("cookie fingerprint: %d %s", rec.Code, rec.Body.String())
	}
	token := authCookie(rec, CookieName)
	if token == nil || token.Value == "" {
		t.Fatal("session cookie missing")
	}
	claims, err := parseJWT(token.Value)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := rd.Get(ctx, sessKey(uid)).Result()
	if err != nil || marker != claims.SID {
		t.Fatalf("session marker not bound: %q %q %v", marker, claims.SID, err)
	}
	if rec := call(`{"uuid":"`+legacyUUID+`"}`, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("legacy account UUID-only login: %d", rec.Code)
	}
	if rec := call(`{"uuid":"`+legacyUUID+`","fingerprint":"new-fp"}`, ""); rec.Code != http.StatusOK {
		t.Fatalf("legacy account credential binding: %d %s", rec.Code, rec.Body.String())
	}
}

func TestE2ESessionBindingRefreshAndDeletedAccount(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-0123456789abcdef0123456789")
	ctx, pg, rd, svc := liveAuth(t)
	var uid string
	if err := pg.QueryRow(ctx, `INSERT INTO users (anon_uuid) VALUES (gen_random_uuid()) RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	tok, err := svc.issueSession(ctx, uid, UserTTL)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := parseJWT(tok)
	if err != nil {
		t.Fatal(err)
	}
	protected := svc.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	call := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/v1/me", nil)
		req.AddCookie(&http.Cookie{Name: CookieName, Value: token})
		rec := httptest.NewRecorder()
		protected.ServeHTTP(rec, req)
		return rec
	}
	if err := rd.Set(ctx, sessKey(uid), "wrong-marker", UserTTL).Err(); err != nil {
		t.Fatal(err)
	}
	if rec := call(tok); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong session marker accepted: %d", rec.Code)
	}
	if err := rd.Set(ctx, sessKey(uid), claims.SID, UserTTL).Err(); err != nil {
		t.Fatal(err)
	}
	csrf, err := svc.csrfFor(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/auth/refresh", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: tok})
	req.Header.Set("X-CSRF", csrf)
	rec := httptest.NewRecorder()
	svc.RequireAuth(http.HandlerFunc(svc.HandleRefresh)).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh: %d %s", rec.Code, rec.Body.String())
	}
	newCookie := authCookie(rec, CookieName)
	if newCookie == nil {
		t.Fatal("refresh cookie missing")
	}
	newClaims, err := parseJWT(newCookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	if newClaims.SID == claims.SID || newClaims.JTI == claims.JTI {
		t.Fatal("refresh did not rotate sid and jti")
	}
	if rec := call(tok); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old token accepted after refresh: %d", rec.Code)
	}
	if rec := call(newCookie.Value); rec.Code != http.StatusOK {
		t.Fatalf("new token rejected: %d", rec.Code)
	}
	if _, err := pg.Exec(ctx, `DELETE FROM users WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	rec = call(newCookie.Value)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("deleted account accepted: %d", rec.Code)
	}
	if authCookie(rec, CookieName) == nil || authCookie(rec, FpCookie) == nil || authCookie(rec, "taro_csrf") == nil {
		t.Fatal("stale auth cookies were not expired")
	}
}

func TestE2ETelegramLoginIsAtomicAndRaceSafe(t *testing.T) {
	t.Setenv("TG_BOT_TOKEN", "test-token")
	ctx, pg, _, svc := liveAuth(t)
	tgID := time.Now().UnixNano()
	initData := craft(t, "test-token", tgID)
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM trial_grants WHERE tg_id=$1`, tgID)
		_, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE tg_id=$1`, tgID)
	})
	results := make(chan struct {
		id    string
		isNew bool
		trial int
		err   error
	}, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, isNew, trial, err := svc.TelegramLogin(ctx, initData, "trial-fp")
			results <- struct {
				id    string
				isNew bool
				trial int
				err   error
			}{id, isNew, trial, err}
		}()
	}
	wg.Wait()
	close(results)
	var firstID string
	newCount, trialCount := 0, 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if firstID == "" {
			firstID = result.id
		}
		if result.id != firstID {
			t.Fatalf("race created multiple users: %q %q", firstID, result.id)
		}
		if result.isNew {
			newCount++
		}
		if result.trial > 0 {
			trialCount++
		}
	}
	if newCount != 1 || trialCount != 1 {
		t.Fatalf("race result: new=%d trial=%d", newCount, trialCount)
	}
	var users, grants, subs int
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE tg_id=$1`, tgID).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM trial_grants WHERE tg_id=$1`, tgID).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM subscriptions s JOIN users u ON u.id=s.user_id WHERE u.tg_id=$1`, tgID).Scan(&subs); err != nil {
		t.Fatal(err)
	}
	if users != 1 || grants != 1 || subs != 1 {
		t.Fatalf("non-atomic trial state: users=%d grants=%d subs=%d", users, grants, subs)
	}
}

func TestE2EMergeRejectsReferralCycle(t *testing.T) {
	ctx, pg, _, svc := liveAuth(t)
	tgID := time.Now().UnixNano() - 2000000
	var survivor, loser, other string
	if err := pg.QueryRow(ctx, `INSERT INTO users (tg_id, fingerprint) VALUES ($1,'cycle-fp') RETURNING id`, tgID).Scan(&survivor); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `INSERT INTO users (anon_uuid, fingerprint) VALUES (gen_random_uuid(),'cycle-fp') RETURNING id`).Scan(&loser); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `INSERT INTO users (anon_uuid) VALUES (gen_random_uuid()) RETURNING id`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM trial_grants WHERE tg_id=$1`, tgID)
		_, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id IN ($1,$2,$3)`, survivor, loser, other)
	})
	if _, err := pg.Exec(ctx, `INSERT INTO referrals (referrer_id, referee_id, code) VALUES ($1,$2,$3)`, loser, other, "C1"+fmt.Sprint(tgID)[1:]); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `INSERT INTO referrals (referrer_id, referee_id, code) VALUES ($1,$2,$3)`, other, loser, "C2"+fmt.Sprint(tgID)[1:]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Link(ctx, loser, tgID, "cycle-fp"); !errors.Is(err, errMergeCycle) {
		t.Fatalf("cycle merge error: %v", err)
	}
	var users int
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE id IN ($1,$2)`, survivor, loser).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 2 {
		t.Fatalf("cycle merge was destructive: %d users", users)
	}
}

func TestE2EMergePreservesMetadataReferralAndQuota(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-0123456789abcdef0123456789")
	ctx, pg, rd, svc := liveAuth(t)
	tgID := time.Now().UnixNano() - 1000000
	var survivor, loser, referrer string
	if err := pg.QueryRow(ctx, `INSERT INTO users (tg_id, fingerprint, referral_code) VALUES ($1,'merge-fp',$2) RETURNING id`, tgID, "SRV"+fmt.Sprint(tgID)[1:]).Scan(&survivor); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `INSERT INTO users (anon_uuid, fingerprint, role, age_confirmed_at, referral_code) VALUES (gen_random_uuid(),'merge-fp','admin',now(),$1) RETURNING id`, "LOS"+fmt.Sprint(tgID)[1:]).Scan(&loser); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `INSERT INTO users (anon_uuid) VALUES (gen_random_uuid()) RETURNING id`).Scan(&referrer); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM trial_grants WHERE tg_id=$1`, tgID)
		_, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id IN ($1,$2,$3)`, survivor, loser, referrer)
		_ = rd.Del(context.Background(), "ent:"+survivor+":merge", "ent:"+loser+":merge").Err()
	})
	if _, err := pg.Exec(ctx, `INSERT INTO referrals (referrer_id, referee_id, code, status, bonus_days) VALUES ($1,$2,$3,'pending',3)`, referrer, loser, "RC"+fmt.Sprint(tgID)[1:]); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `INSERT INTO referrals (referrer_id, referee_id, code, status, bonus_days) VALUES ($1,$2,$3,'completed',5)`, referrer, survivor, "RS"+fmt.Sprint(tgID)[1:]); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `INSERT INTO entitlements (user_id, free_used_today, free_date, love_used_week, love_week) VALUES ($1,3,CURRENT_DATE,2,CURRENT_DATE),($2,1,CURRENT_DATE,4,CURRENT_DATE)`, survivor, loser); err != nil {
		t.Fatal(err)
	}
	if err := rd.Set(ctx, "ent:"+survivor+":merge", 1, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if err := rd.Set(ctx, "ent:"+loser+":merge", 4, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	mergedID, merged, err := svc.Link(ctx, loser, tgID, "merge-fp")
	if err != nil {
		t.Fatal(err)
	}
	if !merged || mergedID != survivor {
		t.Fatalf("merge result: %q %v", mergedID, merged)
	}
	var anon, fp, role, code string
	var age bool
	if err := pg.QueryRow(ctx, `SELECT COALESCE(anon_uuid::text,''), fingerprint, role, age_confirmed_at IS NOT NULL, COALESCE(referral_code,'') FROM users WHERE id=$1`, survivor).Scan(&anon, &fp, &role, &age, &code); err != nil {
		t.Fatal(err)
	}
	if anon == "" || fp != "merge-fp" || role != "admin" || !age || code != "SRV"+fmt.Sprint(tgID)[1:] {
		t.Fatalf("metadata lost: anon=%q fp=%q role=%q age=%v code=%q", anon, fp, role, age, code)
	}
	var referrals int
	if err := pg.QueryRow(ctx, `SELECT COUNT(*) FROM referrals WHERE referee_id=$1 AND referrer_id<>referee_id`, survivor).Scan(&referrals); err != nil {
		t.Fatal(err)
	}
	if referrals != 1 {
		t.Fatalf("referral conflict not resolved: %d", referrals)
	}
	quota, err := rd.Get(ctx, "ent:"+survivor+":merge").Int()
	if err != nil || quota != 4 {
		t.Fatalf("redis quota not merged: %d %v", quota, err)
	}
	msk := time.FixedZone("MSK", 3*60*60)
	local := time.Now().In(msk)
	year, week := local.ISOWeek()
	daily, err := rd.Get(ctx, "ent:"+survivor+":"+local.Format("2006-01-02")).Int()
	if err != nil {
		t.Fatal(err)
	}
	love, err := rd.Get(ctx, "ent:"+survivor+":love:"+fmt.Sprintf("%d-W%d", year, week)).Int()
	if err != nil {
		t.Fatal(err)
	}
	if daily < 3 || love < 4 {
		t.Fatalf("redis quota floor missing: daily=%d love=%d", daily, love)
	}
}

func TestE2EMergePreservesAuthorizationReceiptOwnership(t *testing.T) {
	ctx, pg, rd, svc := liveAuth(t)
	tgID := time.Now().UnixNano() - 3000000
	var survivor, loser string
	if err := pg.QueryRow(ctx, `INSERT INTO users (tg_id, fingerprint) VALUES ($1,'receipt-merge-fp') RETURNING id`, tgID).Scan(&survivor); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `INSERT INTO users (anon_uuid, fingerprint) VALUES (gen_random_uuid(),'receipt-merge-fp') RETURNING id`).Scan(&loser); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM trial_grants WHERE tg_id=$1`, tgID)
		_, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id IN ($1,$2)`, survivor, loser)
		_ = rd.Del(context.Background(), "ent:"+survivor+":*", "ent:"+loser+":*").Err()
	})
	cards := `[{"card_id":1,"reversed":false,"position":0}]`
	var readingID string
	if err := pg.QueryRow(ctx, `
		INSERT INTO readings (user_id, spread_code, question, cards, seed, status, quota_state)
		VALUES ($1,'daily','receipt merge',$2,1,'pending','allowed')
		RETURNING id`, loser, cards).Scan(&readingID); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `
		INSERT INTO reading_authorization_receipts (reading_id, user_id, kind)
		VALUES ($1,$2,'legacy')`, readingID, loser); err != nil {
		t.Fatal(err)
	}
	mergedID, merged, err := svc.Link(ctx, loser, tgID, "receipt-merge-fp")
	if err != nil {
		t.Fatal(err)
	}
	if !merged || mergedID != survivor {
		t.Fatalf("merge result: %q %v", mergedID, merged)
	}
	var owner, receiptOwner string
	if err := pg.QueryRow(ctx, `SELECT user_id::text FROM readings WHERE id=$1`, readingID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := pg.QueryRow(ctx, `SELECT user_id::text FROM reading_authorization_receipts WHERE reading_id=$1`, readingID).Scan(&receiptOwner); err != nil {
		t.Fatal(err)
	}
	if owner != survivor || receiptOwner != survivor {
		t.Fatalf("owners reading=%q receipt=%q survivor=%q", owner, receiptOwner, survivor)
	}
}
