// E2E push-хендлеры: prefs/evening/streak/stats/remind (см. D-покрытие).
package push

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"taro/api/internal/auth"
	"taro/api/internal/testutil"
)

func pushClient(t *testing.T) (func(tok, method, path, body string) *httptest.ResponseRecorder, string) {
	t.Helper()
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg)
	au := auth.New(pg, rd)
	r := chi.NewRouter()
	r.Get("/v1/push/public", svc.HandlePublicKey)
	r.With(au.RequireAuth).Post("/v1/push/subscribe", svc.HandleSubscribe)
	r.With(au.RequireAuth).Delete("/v1/push/unsubscribe", svc.HandleUnsubscribe)
	r.With(au.RequireAuth).Get("/v1/push/prefs", svc.HandleGetPrefs)
	r.With(au.RequireAuth).Post("/v1/push/prefs", svc.HandleSetPrefs)
	r.Post("/v1/admin/push-evening", svc.HandleEvening)
	r.Post("/v1/admin/push-streak-risk", svc.HandleStreakRisk)
	r.Get("/v1/admin/push-stats", svc.HandlePushStats)
	r.Post("/v1/admin/remind-expiring", svc.HandleRemindExpiring)
	uid := testutil.NewUser(t, ctx, pg)
	tok, err := auth.IssueJWT(uid, auth.UserTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err := rd.Set(ctx, "sess:"+uid, "x", auth.UserTTL).Err(); err != nil {
		t.Fatal(err)
	}
	do := func(tok, method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if tok != "" {
			req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	return do, tok
}

func TestE2EPushFailedLogCanBeRetried(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	svc := New(pg)
	uid := testutil.NewUser(t, ctx, pg)
	if _, err := pg.Exec(ctx, `INSERT INTO push_logs (user_id, kind, status) VALUES ($1,'evening','failed')`, uid); err != nil {
		t.Fatal(err)
	}
	claimed, claim, err := svc.logOnce(ctx, uid, "evening", "sending")
	if err != nil || !claimed {
		t.Fatalf("failed log was not claimed: claimed=%v err=%v", claimed, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := svc.setPushLogStatus(canceled, uid, "evening", "failed", claim); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := pg.QueryRow(ctx, `SELECT status FROM push_logs WHERE user_id=$1 AND kind='evening'`, uid).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("canceled send status=%s", status)
	}
	claimed, _, err = svc.logOnce(ctx, uid, "evening", "sending")
	if err != nil || !claimed {
		t.Fatalf("failed log remained stranded: claimed=%v err=%v", claimed, err)
	}
}

func TestE2EPushStaleSendingClaimIsReclaimed(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	svc := New(pg)
	uid := testutil.NewUser(t, ctx, pg)
	if _, err := pg.Exec(ctx, `INSERT INTO push_logs (user_id, kind, status, created_at)
		VALUES ($1,'evening','sending',now()-interval '10 minutes')`, uid); err != nil {
		t.Fatal(err)
	}
	claimed, _, err := svc.logOnce(ctx, uid, "evening", "sending")
	if err != nil || !claimed {
		t.Fatalf("stale sending row was not reclaimed: claimed=%v err=%v", claimed, err)
	}
	var status string
	var createdAt time.Time
	if err := pg.QueryRow(ctx, `SELECT status, created_at FROM push_logs
		WHERE user_id=$1 AND kind='evening' ORDER BY created_at DESC LIMIT 1`, uid).Scan(&status, &createdAt); err != nil {
		t.Fatal(err)
	}
	if status != "sending" || createdAt.Before(time.Now().Add(-time.Minute)) {
		t.Fatalf("reclaimed row status=%s created_at=%s", status, createdAt)
	}
	claimed, _, err = svc.logOnce(ctx, uid, "evening", "sending")
	if err != nil || claimed {
		t.Fatalf("fresh sending row was reclaimed: claimed=%v err=%v", claimed, err)
	}
}

func TestE2EPushReclaimedClaimCannotBeOverwritten(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	svc := New(pg)
	uid := testutil.NewUser(t, ctx, pg)
	if _, err := pg.Exec(ctx, `INSERT INTO push_logs (user_id, kind, status, created_at)
		VALUES ($1,'evening','sending',now()-interval '10 minutes')`, uid); err != nil {
		t.Fatal(err)
	}
	_, staleClaim, err := svc.logOnce(ctx, uid, "evening", "sending")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `UPDATE push_logs SET created_at=now()-interval '10 minutes'
		WHERE user_id=$1 AND kind='evening'`, uid); err != nil {
		t.Fatal(err)
	}
	_, freshClaim, err := svc.logOnce(ctx, uid, "evening", "sending")
	if err != nil {
		t.Fatal(err)
	}
	if staleClaim.Equal(freshClaim) {
		t.Fatalf("reclaim reused fencing version %s", freshClaim)
	}
	err = svc.setPushLogStatus(ctx, uid, "evening", "failed", staleClaim)
	if !errors.Is(err, errPushClaimLost) {
		t.Fatalf("stale worker write error=%v", err)
	}
	var status string
	if err := pg.QueryRow(ctx, `SELECT status FROM push_logs
		WHERE user_id=$1 AND kind='evening'`, uid).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "sending" {
		t.Fatalf("stale worker overwrote reclaimed claim: %s", status)
	}
	if err := svc.setPushLogStatus(ctx, uid, "evening", "sent", freshClaim); err != nil {
		t.Fatalf("fresh worker write: %v", err)
	}
	if err := pg.QueryRow(ctx, `SELECT status FROM push_logs
		WHERE user_id=$1 AND kind='evening'`, uid).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "sent" {
		t.Fatalf("fresh worker status=%s", status)
	}
}

func TestE2EPushClaimSurvivesMidnight(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	svc := New(pg)
	uid := testutil.NewUser(t, ctx, pg)
	claimed, claim, err := svc.logOnce(ctx, uid, "evening", "sending")
	if err != nil || !claimed {
		t.Fatalf("claim: claimed=%v err=%v", claimed, err)
	}
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(msk)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, msk)
	beforeMidnight := midnight.AddDate(0, 0, -1).Add(23*time.Hour + 59*time.Minute + 30*time.Second)
	if _, err := pg.Exec(ctx, `UPDATE push_logs SET created_at=$1 WHERE user_id=$2 AND kind='evening'`, beforeMidnight, uid); err != nil {
		t.Fatal(err)
	}
	claim = beforeMidnight
	if err := svc.setPushLogStatus(ctx, uid, "evening", "sent", claim); err != nil {
		t.Fatalf("claim taken before midnight was lost: %v", err)
	}
	var status string
	if err := pg.QueryRow(ctx, `SELECT status FROM push_logs
		WHERE user_id=$1 AND kind='evening'`, uid).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "sent" {
		t.Fatalf("status=%s", status)
	}
}

func TestE2EPushStatusWriteFailureIsVisible(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	svc := New(pg)
	uid := testutil.NewUser(t, ctx, pg)
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `
		INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth)
		VALUES ($1,$2,'p256dh','auth')`, uid, "https://127.0.0.1/status-failure-"+uid); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `INSERT INTO push_preferences (user_id, hour) VALUES ($1,$2)`, uid, time.Now().In(msk).Hour()); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("push status store unavailable")
	svc.logStore = pushLogStoreStub{
		exec: func(_ context.Context, query string, _ ...any) (pgconn.CommandTag, error) {
			if strings.Contains(query, "UPDATE push_logs SET status") {
				return pgconn.CommandTag{}, wantErr
			}
			return pgconn.NewCommandTag("UPDATE 1"), nil
		},
		row: func(context.Context, string, ...any) pgx.Row {
			return pushLogRowStub{createdAt: time.Now()}
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/push-evening", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	svc.HandleEvening(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status failure response: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != "UNAVAILABLE" {
		t.Fatalf("status failure envelope: %s", rec.Body.String())
	}
}

func TestE2EPushLogDependencyFailureIsNotDuplicate(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	svc := New(pg)
	uid := testutil.NewUser(t, ctx, pg)
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `
		INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth)
		VALUES ($1,$2,'p256dh','auth')`, uid, "https://push.example/dependency-"+uid); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(ctx, `INSERT INTO push_preferences (user_id, hour) VALUES ($1,$2)`, uid, time.Now().In(msk).Hour()); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("push log store unavailable")
	svc.logStore = pushLogStoreStub{
		exec: func(context.Context, string, ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, wantErr
		},
		row: func(context.Context, string, ...any) pgx.Row {
			return pushLogRowStub{err: wantErr}
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/push-evening", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	svc.HandleEvening(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("dependency response: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != "UNAVAILABLE" {
		t.Fatalf("dependency envelope: %s", rec.Body.String())
	}
}

func TestE2EPushHandlers(t *testing.T) {
	do, tok := pushClient(t)
	t.Setenv("VAPID_PUBLIC_KEY", "BKApO8VWk4H1WeiqB1IHuJPVHPendNUt7KXbVIdGuhdOq3s2m3du4PMw-WtfuqaQnJIlFML1IBWjKKnNpgpjX6M")

	// public без auth
	if rec := do("", "GET", "/v1/push/public", ""); rec.Code != 200 {
		t.Fatalf("public: %d", rec.Code)
	}
	// prefs без токена → 401
	if rec := do("", "GET", "/v1/push/prefs", ""); rec.Code != 401 {
		t.Fatalf("prefs anon: want 401 got %d", rec.Code)
	}
	// prefs дефолт
	var p map[string]any
	rec := do(tok, "GET", "/v1/push/prefs", "")
	if rec.Code != 200 {
		t.Fatalf("prefs: %d", rec.Code)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p["hour"] != float64(21) || p["quiet"] != false {
		t.Fatalf("prefs default: %v", p)
	}
	// bad hour → 422
	if rec := do(tok, "POST", "/v1/push/prefs", `{"hour":99}`); rec.Code != 422 {
		t.Fatalf("hour: want 422 got %d", rec.Code)
	}
	// set ok
	if rec := do(tok, "POST", "/v1/push/prefs", `{"hour":20,"quiet":true}`); rec.Code != 200 {
		t.Fatalf("set: %d", rec.Code)
	}
	// subscribe bad → 422; SSRF (169.254, http, 127.0.0.1) → 422 (см. S02)
	for _, bad := range []string{
		`{"endpoint":"http://169.254.169.254/x","p256dh":"AA","auth":"BB"}`,
		`{"endpoint":"http://example.com/x","p256dh":"AA","auth":"BB"}`,
		`{"endpoint":"https://127.0.0.1:8081/x","p256dh":"AA","auth":"BB"}`,
		`{"endpoint":"https://user:pass@push.example/x","p256dh":"AA","auth":"BB"}`,
	} {
		if rec := do(tok, "POST", "/v1/push/subscribe", bad); rec.Code != 422 {
			t.Fatalf("ssrf %s: want 422 got %d", bad[:40], rec.Code)
		}
	}
	sub := `{"endpoint":"https://example.com/e2e1","p256dh":"BAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","auth":"AAAAAAAAAAAAAAAAAAAAAA"}`
	if rec := do(tok, "POST", "/v1/push/subscribe", sub); rec.Code != 200 {
		t.Fatalf("sub: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(tok, "DELETE", "/v1/push/unsubscribe", `{"endpoint":"https://example.com/e2e1"}`); rec.Code != 200 {
		t.Fatalf("unsub: %d", rec.Code)
	}
	// вечерняя/стрик/реминдер/статы без подписок — ok с нулями
	for _, path := range []string{"/v1/admin/push-evening", "/v1/admin/push-streak-risk", "/v1/admin/remind-expiring"} {
		if rec := do("", "POST", path, `{}`); rec.Code != 200 {
			t.Fatalf("%s: want 200 got %d: %s", path, rec.Code, rec.Body.String())
		}
	}
	if rec := do("", "GET", "/v1/admin/push-stats", ""); rec.Code != 200 {
		t.Fatalf("stats: %d", rec.Code)
	}
}
