// A17/F-43: выделение роли ai_cache. Ошибки разбора адреса обязаны быть
// громкими (main.go печатает предупреждение и деградирует), а не приводить к
// молчаливому возврату на общий инстанс.
package store

import (
	"os"
	"strings"
	"testing"
)

func TestConnectRedisAISharedWithoutAddress(t *testing.T) {
	t.Setenv("REDIS_AI_ADDR", "")
	t.Setenv("REDIS_ADDR", "127.0.0.1:6399")
	client, shared, err := ConnectRedisAI()
	if err != nil {
		t.Fatal(err)
	}
	if !shared {
		t.Fatal("without REDIS_AI_ADDR the role must be reported as shared, so the caller can warn")
	}
	if client == nil {
		t.Fatal("shared mode must still return a usable client")
	}
	_ = client.Close()
}

func TestConnectRedisAISeparateRole(t *testing.T) {
	cases := []struct {
		name    string
		addr    string
		wantErr string
	}{
		{name: "separate instance", addr: "cache-ai:6379"},
		{name: "separate database", addr: "127.0.0.1:6379/1"},
		{name: "database zero is explicit", addr: "127.0.0.1:6379/0"},
		{name: "index too big", addr: "127.0.0.1:6379/99", wantErr: "bad database index"},
		{name: "index not a number", addr: "127.0.0.1:6379/abc", wantErr: "bad database index"},
		{name: "negative index", addr: "127.0.0.1:6379/-1", wantErr: "bad database index"},
		{name: "empty host", addr: "/1", wantErr: "empty host"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("REDIS_AI_ADDR", tc.addr)
			client, shared, err := ConnectRedisAI()
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("addr %q must be rejected, got client=%v", tc.addr, client)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q must explain %q", err, tc.wantErr)
				}
				if shared || client != nil {
					t.Fatalf("rejected address must not fall back to a shared client: shared=%v client=%v", shared, client)
				}
				return
			}
			if err != nil || shared || client == nil {
				t.Fatalf("addr %q must yield a detached role: shared=%v err=%v client=%v", tc.addr, shared, err, client)
			}
			_ = client.Close()
		})
	}
}

func TestConnectRedisAIFallsBackToMainPassword(t *testing.T) {
	t.Setenv("REDIS_AI_ADDR", "127.0.0.1:6379/2")
	t.Setenv("REDIS_AI_PASSWORD", "")
	t.Setenv("REDIS_PASSWORD", "")
	client, shared, err := ConnectRedisAI()
	if err != nil || shared || client == nil {
		t.Fatalf("addr without its own password must still connect: shared=%v err=%v", shared, err)
	}
	_ = client.Close()
	if os.Getenv("REDIS_AI_PASSWORD") == "REDIS_PASSWORD" {
		t.Fatal("sanity: the fixture must not accidentally set an identical password")
	}
}
