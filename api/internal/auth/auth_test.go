// Тест проверки initData (см. 02-functional/02-auth.md GWT).
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func craftInitData(t *testing.T, botToken, userJSON string, authDate int64) string {
	t.Helper()
	q := url.Values{}
	q.Set("user", userJSON)
	q.Set("auth_date", strconv.FormatInt(authDate, 10))
	q.Set("query_id", "test")
	pairs := []string{}
	for k, vv := range q {
		pairs = append(pairs, k+"="+strings.Join(vv, ","))
	}
	sort.Strings(pairs)
	macKey := hmac.New(sha256.New, []byte("WebAppData"))
	macKey.Write([]byte(botToken))
	mac := hmac.New(sha256.New, macKey.Sum(nil))
	mac.Write([]byte(strings.Join(pairs, "\n")))
	q.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return q.Encode()
}

func TestVerifyInitDataOK(t *testing.T) {
	initData := craftInitData(t, "test-token", `{"id":12345}`, time.Now().Unix())
	id, err := VerifyInitData(initData, "test-token")
	if err != nil {
		t.Fatalf("valid initData rejected: %v", err)
	}
	if id != 12345 {
		t.Fatalf("wrong tg id: %d", id)
	}
}

func TestVerifyInitDataBadHash(t *testing.T) {
	initData := craftInitData(t, "test-token", `{"id":12345}`, time.Now().Unix())
	if _, err := VerifyInitData(initData, "wrong-token"); err == nil {
		t.Fatal("forged initData accepted")
	}
}

func TestVerifyInitDataStale(t *testing.T) {
	initData := craftInitData(t, "test-token", `{"id":12345}`, time.Now().Add(-25*time.Hour).Unix())
	if _, err := VerifyInitData(initData, "test-token"); err == nil {
		t.Fatal("stale initData accepted")
	}
}
