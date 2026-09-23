// Unit apierr + store (см. D-покрытие).
package apierr

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, 402, CodeLimitSkip, "Лимит")
	if rec.Code != 402 {
		t.Fatalf("code: %d", rec.Code)
	}
	var body map[string]map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"]["code"] != CodeLimitSkip || body["error"]["message_ru"] != "Лимит" {
		t.Fatalf("body: %s", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("ct: %s", ct)
	}
}
