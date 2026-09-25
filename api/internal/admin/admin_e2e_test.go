package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"taro/api/internal/auth"
	"taro/api/internal/testutil"
)

const testAdminPassword = "correct horse battery staple 2026!"

func adminSetup(t *testing.T) (*chi.Mux, string, *pgxpool.Pool, string) {
	t.Helper()
	t.Setenv("ADMIN_ORIGIN", defaultAdminOrigin)
	ctx, pg, rd := testutil.Live(t)
	svc := New(pg, rd)
	r := chi.NewRouter()
	r.Post("/v1/admin/login", svc.HandleLogin)
	r.With(svc.RequireAdmin).Post("/v1/admin/logout", svc.HandleLogout)
	r.With(svc.RequireAdmin).Get("/v1/admin/config", svc.HandleGetConfig)
	r.With(svc.RequireAdmin).Post("/v1/admin/config/publish", svc.HandlePublish)
	r.With(svc.RequireAdmin).Post("/v1/admin/rotate-seasonal", svc.HandleRotateSeasonal)

	var uid string
	if err := pg.QueryRow(ctx, `INSERT INTO users (role) VALUES ('admin') RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	username := fmt.Sprintf("test-admin-%d", time.Now().UnixNano())
	hash, err := HashPassword(testAdminPassword)
	if err != nil {
		t.Fatal(err)
	}
	var sessionVersion int64
	if err := pg.QueryRow(ctx, `
		INSERT INTO admin_accounts (user_id, username, password_hash)
		VALUES ($1, $2, $3)
		RETURNING session_version`, uid, username, hash).Scan(&sessionVersion); err != nil {
		t.Fatal(err)
	}
	sid, err := newAdminSessionID()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM admin_audit WHERE admin_id=$1`, uid)
		_, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, uid)
		var cursor uint64
		for {
			keys, next, err := rd.Scan(context.Background(), cursor, "sess:admin:"+uid+":*", 100).Result()
			if err != nil {
				break
			}
			if len(keys) > 0 {
				_, _ = rd.Del(context.Background(), keys...).Result()
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
		_, _ = rd.Del(context.Background(), adminSessionKey(uid, sid), legacyAdminSessionKey(uid)).Result()
	})
	tok, err := auth.IssueJWT("admin:"+uid, AdminTTL, sid)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := auth.ParseJWTClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	if err := rd.Set(ctx, adminSessionKey(uid, sid), adminSessionMarker(claims.JTI, sessionVersion), AdminTTL).Err(); err != nil {
		t.Fatal(err)
	}
	return r, tok, pg, username
}

func acallWithHeaders(r *chi.Mux, tok, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.AddCookie(&http.Cookie{Name: AdminCookie, Value: tok})
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func acall(r *chi.Mux, tok, method, path, body string) *httptest.ResponseRecorder {
	return acallWithHeaders(r, tok, method, path, body, nil)
}

func loginBody(username, password string) string {
	raw, _ := json.Marshal(loginRequest{Username: username, Password: password})
	return string(raw)
}

func TestE2EAdminAuth(t *testing.T) {
	r, tok, _, _ := adminSetup(t)
	if rec := acall(r, "", "GET", "/v1/admin/config", ""); rec.Code != 403 {
		t.Fatalf("no cookie: want 403 got %d", rec.Code)
	}
	if rec := acall(r, "junk", "GET", "/v1/admin/config", ""); rec.Code != 403 {
		t.Fatalf("junk token: want 403 got %d", rec.Code)
	}
	rec := acall(r, tok, "GET", "/v1/admin/config", "")
	if rec.Code != 200 {
		t.Fatalf("config: want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	var cfg map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"app_config", "plans", "spreads"} {
		if cfg[key] == nil {
			t.Fatalf("config missing %s", key)
		}
	}
}

func TestE2EAdminLogin(t *testing.T) {
	r, _, _, username := adminSetup(t)
	headers := map[string]string{"X-Real-IP": "198.51.100.41", "Origin": defaultAdminOrigin}
	if rec := acallWithHeaders(r, "", "POST", "/v1/admin/login", loginBody(username, "wrong-password"), headers); rec.Code != 401 {
		t.Fatalf("bad password: want 401 got %d: %s", rec.Code, rec.Body.String())
	}
	rec := acallWithHeaders(r, "", "POST", "/v1/admin/login", loginBody(strings.ToUpper(username), testAdminPassword), headers)
	if rec.Code != 200 {
		t.Fatalf("login: want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	var adminCookie *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == AdminCookie {
			adminCookie = cookie
		}
	}
	if adminCookie == nil || adminCookie.Value == "" || !adminCookie.HttpOnly || !adminCookie.Secure || adminCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("invalid admin cookie: %#v", adminCookie)
	}
	cfg := acallWithHeaders(r, adminCookie.Value, "GET", "/v1/admin/config", "", nil)
	if cfg.Code != 200 {
		t.Fatalf("authenticated config: %d %s", cfg.Code, cfg.Body.String())
	}
	logout := acallWithHeaders(r, adminCookie.Value, "POST", "/v1/admin/logout", `{}`, map[string]string{"Origin": defaultAdminOrigin})
	if logout.Code != 200 {
		t.Fatalf("logout: %d %s", logout.Code, logout.Body.String())
	}
	if cfg = acallWithHeaders(r, adminCookie.Value, "GET", "/v1/admin/config", "", nil); cfg.Code != 403 {
		t.Fatalf("revoked session: want 403 got %d", cfg.Code)
	}
}

func TestE2EAdminLogoutLoginRejectsOldToken(t *testing.T) {
	r, _, _, username := adminSetup(t)
	n := time.Now().UnixNano()
	headers := map[string]string{
		"Origin":    defaultAdminOrigin,
		"X-Real-IP": fmt.Sprintf("198.18.%d.%d", (n>>8)&0xff, n&0xff),
	}
	first := acallWithHeaders(r, "", "POST", "/v1/admin/login", loginBody(username, testAdminPassword), headers)
	if first.Code != 200 {
		t.Fatalf("first login: %d %s", first.Code, first.Body.String())
	}
	var oldCookie *http.Cookie
	for _, cookie := range first.Result().Cookies() {
		if cookie.Name == AdminCookie {
			oldCookie = cookie
		}
	}
	if oldCookie == nil || oldCookie.Value == "" {
		t.Fatal("first admin cookie missing")
	}
	if rec := acallWithHeaders(r, oldCookie.Value, "POST", "/v1/admin/logout", `{}`, headers); rec.Code != 200 {
		t.Fatalf("logout: %d %s", rec.Code, rec.Body.String())
	}
	second := acallWithHeaders(r, "", "POST", "/v1/admin/login", loginBody(username, testAdminPassword), headers)
	if second.Code != 200 {
		t.Fatalf("second login: %d %s", second.Code, second.Body.String())
	}
	var newCookie *http.Cookie
	for _, cookie := range second.Result().Cookies() {
		if cookie.Name == AdminCookie {
			newCookie = cookie
		}
	}
	if newCookie == nil || newCookie.Value == "" {
		t.Fatal("new admin cookie missing")
	}
	if old := acall(r, oldCookie.Value, "GET", "/v1/admin/config", ""); old.Code != 403 {
		t.Fatalf("old token accepted after relogin: %d %s", old.Code, old.Body.String())
	}
	if current := acall(r, newCookie.Value, "GET", "/v1/admin/config", ""); current.Code != 200 {
		t.Fatalf("new token rejected: %d %s", current.Code, current.Body.String())
	}
}

func TestE2EAdminLogoutRedisFailure(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-0123456789abcdef0123456789")
	broken := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  10 * time.Millisecond,
		ReadTimeout:  10 * time.Millisecond,
		WriteTimeout: 10 * time.Millisecond,
		MaxRetries:   -1,
	})
	t.Cleanup(func() { _ = broken.Close() })
	sid := "0123456789abcdef0123456789abcdef"
	tok, err := auth.IssueJWT("admin:00000000-0000-0000-0000-000000000001", AdminTTL, sid)
	if err != nil {
		t.Fatal(err)
	}
	svc := New(nil, broken)
	req := httptest.NewRequest("POST", "/v1/admin/logout", strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: AdminCookie, Value: tok})
	rec := httptest.NewRecorder()
	svc.HandleLogout(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("logout Redis failure: %d %s", rec.Code, rec.Body.String())
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("logout cleared cookie after failed revocation")
	}
}

func TestE2EAdminLoginSecurity(t *testing.T) {
	r, _, pg, username := adminSetup(t)
	unique := fmt.Sprintf("unknown-%d", time.Now().UnixNano())
	headers := map[string]string{"X-Real-IP": "198.51.100.52"}
	if rec := acallWithHeaders(r, "", "POST", "/v1/admin/login", loginBody(unique, "wrong-password"), headers); rec.Code != 401 {
		t.Fatalf("unknown user: want 401 got %d", rec.Code)
	}
	for name, credentials := range map[string]loginRequest{
		"invalid username":  {Username: "invalid username", Password: dummyAdminPassword},
		"password too long": {Username: username, Password: testAdminPassword + "x"},
	} {
		if rec := acallWithHeaders(r, "", "POST", "/v1/admin/login", loginBody(credentials.Username, credentials.Password), headers); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: want 401 got %d: %s", name, rec.Code, rec.Body.String())
		}
	}
	if rec := acallWithHeaders(r, "", "POST", "/v1/admin/login", `{"initData":"telegram-data"}`, headers); rec.Code != 422 {
		t.Fatalf("legacy initData: want 422 got %d", rec.Code)
	}
	if rec := acallWithHeaders(r, "", "POST", "/v1/admin/login", loginBody(username, testAdminPassword), map[string]string{"Origin": "https://evil.example"}); rec.Code != 403 {
		t.Fatalf("external origin: want 403 got %d", rec.Code)
	}
	if _, err := pg.Exec(context.Background(), `UPDATE admin_accounts SET is_active=false WHERE username=$1`, username); err != nil {
		t.Fatal(err)
	}
	if rec := acallWithHeaders(r, "", "POST", "/v1/admin/login", loginBody(username, testAdminPassword), map[string]string{"X-Real-IP": "198.51.100.53"}); rec.Code != 401 {
		t.Fatalf("inactive account: want 401 got %d", rec.Code)
	}
}

func TestE2EAdminOriginGate(t *testing.T) {
	r, tok, _, username := adminSetup(t)
	if rec := acallWithHeaders(r, "", "POST", "/v1/admin/login", loginBody(username, testAdminPassword), nil); rec.Code != http.StatusOK {
		t.Fatalf("login without origin: want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	cases := []struct {
		name    string
		headers map[string]string
	}{
		{name: "missing"},
		{name: "port", headers: map[string]string{"Origin": "http://localhost:8082"}},
		{name: "scheme", headers: map[string]string{"Origin": "https://localhost:8081"}},
		{name: "host", headers: map[string]string{"Origin": "http://localhost:8081"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := acallWithHeaders(r, tok, "POST", "/v1/admin/logout", `{}`, tc.headers)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("origin: want 403 got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
	rec := acallWithHeaders(r, tok, "POST", "/v1/admin/logout", `{}`, map[string]string{"Origin": defaultAdminOrigin})
	if rec.Code != http.StatusOK {
		t.Fatalf("configured origin: want 200 got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestE2EAdminSessionVersionRejectsRotatedCredential(t *testing.T) {
	r, tok, pg, username := adminSetup(t)
	newHash, err := HashPassword("new correct horse battery staple 2026!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Exec(context.Background(), `
		UPDATE admin_accounts
		SET password_hash=$1, session_version=session_version+1, updated_at=now()
		WHERE username=$2`, newHash, username); err != nil {
		t.Fatal(err)
	}
	if rec := acall(r, tok, "GET", "/v1/admin/config", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("rotated session: want 403 got %d: %s", rec.Code, rec.Body.String())
	}
	rec := acallWithHeaders(r, "", "POST", "/v1/admin/login", loginBody(username, "new correct horse battery staple 2026!"), map[string]string{"Origin": defaultAdminOrigin})
	if rec.Code != http.StatusOK {
		t.Fatalf("new login: want 200 got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDefaultAdminOrigin(t *testing.T) {
	t.Setenv("ADMIN_ORIGIN", "")
	req := httptest.NewRequest("POST", defaultAdminOrigin+"/v1/admin/logout", nil)
	if defaultAdminOrigin != "http://127.0.0.1:8081" {
		t.Fatalf("default admin origin: %s", defaultAdminOrigin)
	}
	if !isLocalOrigin(req, defaultAdminOrigin) {
		t.Fatal("default admin origin rejected")
	}
	if isLocalOrigin(req, "http://localhost:8081") {
		t.Fatal("localhost accepted as default admin origin")
	}
}

func TestAdminOriginConfiguration(t *testing.T) {
	t.Setenv("ADMIN_ORIGIN", "http://localhost:9090")
	req := httptest.NewRequest("POST", "http://localhost:9090/v1/admin/logout", nil)
	if !isLocalOrigin(req, "http://localhost:9090") {
		t.Fatal("configured admin origin rejected")
	}
	for _, origin := range []string{"", "http://localhost:8081", "https://localhost:9090", "http://127.0.0.1:9090", "http://localhost:9091"} {
		if isLocalOrigin(req, origin) {
			t.Fatalf("mismatched admin origin accepted: %q", origin)
		}
	}
}

func TestE2EAdminLoginRateLimit(t *testing.T) {
	r, _, _, _ := adminSetup(t)
	n := time.Now().UnixNano()
	ip := fmt.Sprintf("198.18.%d.%d", (n>>8)&0xff, n&0xff)
	rateUsername := fmt.Sprintf("rate-%d", n)
	headers := map[string]string{"X-Real-IP": ip}
	for i := 0; i < adminLoginRateMax; i++ {
		if rec := acallWithHeaders(r, "", "POST", "/v1/admin/login", loginBody(rateUsername, "wrong-password"), headers); rec.Code != 401 {
			t.Fatalf("attempt %d: want 401 got %d", i+1, rec.Code)
		}
	}
	if rec := acallWithHeaders(r, "", "POST", "/v1/admin/login", loginBody(rateUsername, "wrong-password"), headers); rec.Code != 429 {
		t.Fatalf("rate limit: want 429 got %d", rec.Code)
	}
}

func TestE2EAdminLoginRateWindowIsFixed(t *testing.T) {
	t.Setenv("ADMIN_ORIGIN", defaultAdminOrigin)
	ctx, _, rd := testutil.Live(t)
	svc := New(nil, rd)
	req := httptest.NewRequest("POST", "http://127.0.0.1:8081/v1/admin/login", nil)
	req.RemoteAddr = "198.19.7.21:4321"
	username := fmt.Sprintf("window-%d", time.Now().UnixNano())
	keys := adminLoginRateKeys(req, username)
	t.Cleanup(func() {
		for _, key := range keys {
			_ = rd.Del(context.Background(), key).Err()
		}
	})
	for _, key := range keys {
		if err := rd.Set(ctx, key, 1, 90*time.Second).Err(); err != nil {
			t.Fatal(err)
		}
	}
	allowed, err := svc.allowLogin(req, username)
	if err != nil || !allowed {
		t.Fatalf("first attempt: allowed=%v err=%v", allowed, err)
	}
	for i, key := range keys {
		ttl, err := rd.TTL(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
		if ttl <= 0 || ttl > 90*time.Second {
			t.Fatalf("key %d window was extended: ttl=%s", i, ttl)
		}
	}
	allowed, err = svc.allowLogin(req, username)
	if err != nil || !allowed {
		t.Fatalf("second attempt: allowed=%v err=%v", allowed, err)
	}
	for i, key := range keys {
		ttl, err := rd.TTL(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
		if ttl <= 0 || ttl > 90*time.Second {
			t.Fatalf("key %d window was extended on repeat: ttl=%s", i, ttl)
		}
	}
}

func TestPasswordHash(t *testing.T) {
	hash, err := HashPassword(testAdminPassword)
	if err != nil {
		t.Fatal(err)
	}
	svc := New(nil, nil)
	if hash == testAdminPassword || !svc.verifyPassword(hash, testAdminPassword) || svc.verifyPassword(hash, "wrong-password") {
		t.Fatal("password hash verification failed")
	}
	maxPassword := strings.Repeat("a", maxPasswordBytes)
	maxHash, err := HashPassword(maxPassword)
	if err != nil {
		t.Fatal(err)
	}
	if svc.verifyPassword(maxHash, maxPassword+"x") {
		t.Fatal("password longer than bcrypt limit accepted")
	}
	submitted := "submitted-password-2026!"
	submittedHash, err := HashPassword(submitted)
	if err != nil {
		t.Fatal(err)
	}
	svc.dummyHash = []byte(submittedHash)
	if !svc.verifyPassword("", submitted) {
		t.Fatal("submitted password was not compared with dummy hash")
	}
	for _, password := range []string{"short", "contains\x00null", string(make([]byte, maxPasswordBytes+1))} {
		if _, err := HashPassword(password); err == nil {
			t.Fatalf("weak password accepted: %q", password)
		}
	}
}

func runAdminctl(t *testing.T, userID, username, password string) {
	t.Helper()
	cmd := exec.Command("go", "run", "../../cmd/adminctl", "-username", username, "-user-id", userID)
	cmd.Stdin = strings.NewReader(password + "\n")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("adminctl: %v: %s", err, output)
	}
}

func assertAdminctlAudit(t *testing.T, pg *pgxpool.Pool, userID, username, action, password, hash string) {
	t.Helper()
	var count int
	if err := pg.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM admin_audit
		WHERE admin_id=$1 AND action=$2`, userID, action).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("%s audit rows: %d", action, count)
	}
	var raw string
	if err := pg.QueryRow(context.Background(), `
		SELECT diff::text FROM admin_audit
		WHERE admin_id=$1 AND action=$2`, userID, action).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var diff map[string]string
	if err := json.Unmarshal([]byte(raw), &diff); err != nil {
		t.Fatal(err)
	}
	if len(diff) != 1 || diff["username"] != username {
		t.Fatalf("unsafe or unexpected %s audit diff: %s", action, raw)
	}
	for _, secret := range []string{password, hash, "$2a$", "$2b$", "$2y$"} {
		if secret != "" && strings.Contains(raw, secret) {
			t.Fatalf("%s audit diff contains credential material", action)
		}
	}
}

func TestE2EAdminctlCredentialAudit(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	var userID string
	if err := pg.QueryRow(ctx, `INSERT INTO users (role) VALUES ('admin') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.Exec(context.Background(), `DELETE FROM admin_audit WHERE admin_id=$1`, userID)
		_, _ = pg.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID)
	})
	username := fmt.Sprintf("audit-admin-%d", time.Now().UnixNano())
	provisionPassword := "first-admin-password-2026!"
	rotatePassword := "second-admin-password-2026!"
	reactivatePassword := "third-admin-password-2026!"

	runAdminctl(t, userID, username, provisionPassword)
	var hash string
	var active bool
	var version int64
	if err := pg.QueryRow(ctx, `SELECT password_hash, is_active, session_version FROM admin_accounts WHERE user_id=$1`, userID).Scan(&hash, &active, &version); err != nil {
		t.Fatal(err)
	}
	if !active || version != 1 {
		t.Fatalf("provision state: active=%v version=%d", active, version)
	}
	assertAdminctlAudit(t, pg, userID, username, "admin:provision", provisionPassword, hash)

	runAdminctl(t, userID, username, rotatePassword)
	if err := pg.QueryRow(ctx, `SELECT password_hash, is_active, session_version FROM admin_accounts WHERE user_id=$1`, userID).Scan(&hash, &active, &version); err != nil {
		t.Fatal(err)
	}
	if !active || version != 2 {
		t.Fatalf("rotation state: active=%v version=%d", active, version)
	}
	assertAdminctlAudit(t, pg, userID, username, "admin:password:rotate", rotatePassword, hash)

	if _, err := pg.Exec(ctx, `UPDATE admin_accounts SET is_active=false WHERE user_id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	runAdminctl(t, userID, username, reactivatePassword)
	if err := pg.QueryRow(ctx, `SELECT password_hash, is_active, session_version FROM admin_accounts WHERE user_id=$1`, userID).Scan(&hash, &active, &version); err != nil {
		t.Fatal(err)
	}
	if !active || version != 3 {
		t.Fatalf("reactivation state: active=%v version=%d", active, version)
	}
	assertAdminctlAudit(t, pg, userID, username, "admin:reactivate", reactivatePassword, hash)
}

func TestE2EPublishAudit(t *testing.T) {
	r, tok, pg, _ := adminSetup(t)
	if rec := acallWithHeaders(r, tok, "POST", "/v1/admin/config/publish", `{"app_config":{"nope":1}}`, map[string]string{"Origin": defaultAdminOrigin}); rec.Code != 422 {
		t.Fatalf("bad key: want 422 got %d", rec.Code)
	}
	rec := acallWithHeaders(r, tok, "POST", "/v1/admin/config/publish",
		`{"app_config":{"copy.paywall_cta":"E2E"},"spreads":[{"code":"daily","sort_order":11}]}`,
		map[string]string{"Origin": defaultAdminOrigin})
	if rec.Code != 200 {
		t.Fatalf("publish: want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["ok"] != true {
		t.Fatalf("publish not ok: %s", rec.Body.String())
	}
	var audits int
	_ = pg.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM admin_audit WHERE action='config:publish'`).Scan(&audits)
	if audits < 1 {
		t.Fatal("no audit row")
	}
	_, _ = pg.Exec(context.Background(), `UPDATE spreads SET sort_order=10 WHERE code='daily'`)
	_, _ = pg.Exec(context.Background(),
		`UPDATE app_config SET value='"Продолжить безлимитно — 299₽/мес"' WHERE key='copy.paywall_cta'`)
	_, _ = pg.Exec(context.Background(), `DELETE FROM admin_audit WHERE action='config:publish' AND diff::text LIKE '%E2E%'`)
}

func TestValidateConfigValue(t *testing.T) {
	valid := map[string]string{
		"free.daily_limit":   `"1"`,
		"ai":                 `{"model":"openai/gpt-4o-mini","max_tokens":900,"temperature":0.7}`,
		"ab.price_month":     `{"enabled":true,"control":299,"test":349,"split":50}`,
		"offers.winback":     `{"enabled":true,"pct":20}`,
		"trial":              `{"enabled":true,"days":3,"require_tg":true}`,
		"referral":           `{"bonus_days":3,"monthly_cap":30}`,
		"spreads.seasonal":   `[{"code":"fullmoon","from":"2026-01-01","to":"2026-12-31"}]`,
		"payments.yookassa":  `{"enabled":true}`,
		"copy.paywall_title": `"Текст"`,
	}
	for key, value := range valid {
		if !validateConfigValue(key, json.RawMessage(value)) {
			t.Fatalf("valid config rejected: %s=%s", key, value)
		}
	}
	invalid := map[string]string{
		"free.daily_limit": `"many"`,
		"ai":               `{"unknown":true}`,
		"ab.price_month":   `{"control":0}`,
		"trial":            `{"days":0}`,
		"spreads.seasonal": `[{"code":"x","from":"2026-12-31","to":"2026-01-01"}]`,
	}
	for key, value := range invalid {
		if validateConfigValue(key, json.RawMessage(value)) {
			t.Fatalf("invalid config accepted: %s=%s", key, value)
		}
	}
}

func TestE2ERotateSeasonal(t *testing.T) {
	r, tok, pg, _ := adminSetup(t)
	ctx := context.Background()
	if _, err := pg.Exec(ctx, `INSERT INTO app_config (key, value)
		VALUES ('spreads.seasonal','[{"code":"fullmoon","from":"2000-01-01","to":"2100-12-31"}]')
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value`); err != nil {
		t.Fatal(err)
	}
	defer pg.Exec(context.Background(), `DELETE FROM app_config WHERE key='spreads.seasonal'`)
	defer pg.Exec(context.Background(), `UPDATE spreads SET is_active=false WHERE code='fullmoon'`)
	rec := acallWithHeaders(r, tok, "POST", "/v1/admin/rotate-seasonal", `{}`, map[string]string{"Origin": defaultAdminOrigin})
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != 200 || out["ok"] != true {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body.String())
	}
	var active bool
	_ = pg.QueryRow(ctx, `SELECT is_active FROM spreads WHERE code='fullmoon'`).Scan(&active)
	if !active {
		t.Fatal("fullmoon not activated")
	}
}
