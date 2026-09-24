package ai

import (
	"strings"
	"testing"
)

func TestCacheCryptRoundtrip(t *testing.T) {
	t.Setenv("REDIS_ENC_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	sealed, err := sealCache("тайный текст толкования")
	if err != nil || !strings.HasPrefix(sealed, "v1:") || strings.Contains(sealed, "тайный") {
		t.Fatalf("seal: %q err=%v", sealed, err)
	}
	plain, err := openCache(sealed)
	if err != nil || plain != "тайный текст толкования" {
		t.Fatalf("open: %q err=%v", plain, err)
	}
}

func TestCacheCryptPlaintextCompat(t *testing.T) {
	t.Setenv("REDIS_ENC_KEY", "")
	if s, _ := sealCache("abc"); s != "abc" {
		t.Fatalf("no-key seal must passthrough: %q", s)
	}
	if p, _ := openCache("legacy plain"); p != "legacy plain" {
		t.Fatalf("legacy read: %q", p)
	}
	if p, err := openCache("v1:!!!"); err == nil || p != "" {
		t.Fatalf("sealed without key must miss: %q %v", p, err)
	}
	if _, err := openCache(""); err != nil {
		t.Fatalf("empty must not error: %v", err)
	}
}
