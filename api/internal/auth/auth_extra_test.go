package auth

import "testing"

// S01: пустой токен без флага — отказ (fail-closed).
func TestVerifyEmptyTokenClosed(t *testing.T) {
	t.Setenv("TG_ALLOW_EMPTY", "")
	if _, err := VerifyInitData("user=%7B%22id%22%3A1%7D&hash=x", ""); err == nil {
		t.Fatal("empty token must fail")
	}
}
