package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVerifyInitDataRejectsFutureAndShortWindow(t *testing.T) {
	future := craftInitData(t, "test-token", `{"id":12345}`, time.Now().Add(time.Minute).Unix())
	if _, err := VerifyInitData(future, "test-token"); err == nil {
		t.Fatal("future auth_date accepted")
	}
	stale := craftInitData(t, "test-token", `{"id":12345}`, time.Now().Add(-TelegramAuthMaxAge-time.Second).Unix())
	if _, err := VerifyInitData(stale, "test-token"); err == nil {
		t.Fatal("stale auth_date accepted")
	}
}

func TestJWTSessionClaimsKeepAdminSubject(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-0123456789abcdef0123456789")
	tok, err := IssueJWT("admin:00000000-0000-0000-0000-000000000001", time.Hour, "session-id")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := parseJWT(tok)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "admin:00000000-0000-0000-0000-000000000001" {
		t.Fatalf("admin subject changed: %q", claims.Subject)
	}
	if claims.SID != "session-id" || claims.JTI == "" {
		t.Fatalf("session claims missing: %+v", claims)
	}
}

func TestTrialConfigIsBoundedAndConfigDriven(t *testing.T) {
	enabled, days := parseTrialConfig(json.RawMessage(`{"enabled":true,"days":5}`))
	if !enabled || days != 5 {
		t.Fatalf("custom trial config ignored: %v %d", enabled, days)
	}
	enabled, days = parseTrialConfig(json.RawMessage(`{"enabled":false,"days":0}`))
	if enabled || days != 3 {
		t.Fatalf("invalid trial config not handled: %v %d", enabled, days)
	}
}

func TestFingerprintCookieIsPreferredAndValidated(t *testing.T) {
	req := httptest.NewRequest("POST", "/v1/auth/anon", nil)
	req.AddCookie(&http.Cookie{Name: FpCookie, Value: "cookie-fp"})
	if got := fpOf(req, "body-fp"); got != "cookie-fp" {
		t.Fatalf("body fingerprint won over cookie: %q", got)
	}
	if !validFingerprint("fp-A", true) {
		t.Fatal("valid fingerprint rejected")
	}
	for _, fp := range []string{"", " ", "fp\x00A", "fp\nA", string(make([]byte, maxFingerprintLen+1))} {
		if validFingerprint(fp, true) {
			t.Fatalf("invalid fingerprint accepted: %q", fp)
		}
	}
}
