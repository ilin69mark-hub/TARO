// Package apierr — единый формат ошибок (см. docs/project-book/04-architecture/02).
package apierr

import (
	"encoding/json"
	"net/http"
)

// Коды ошибок — единые на весь API (см. 04-api-spec.md).
const (
	CodeValidation   = "VALIDATION"
	CodeRateLimited  = "RATE_LIMITED"
	CodeLimitSkip    = "LIMIT_EXCEEDED"
	CodeInvalidTg    = "INVALID_TG_HASH"
	CodeBadSign      = "BAD_SIGN"
	CodeAITimeout    = "AI_TIMEOUT"
	CodeInternal     = "INTERNAL"
	CodeNotFound     = "NOT_FOUND"
	CodeUnauthorized = "UNAUTHORIZED"
	CodeForbidden    = "FORBIDDEN"
)

type envelope struct {
	Error errBody `json:"error"`
}

type errBody struct {
	Code    string `json:"code"`
	Message string `json:"message_ru"`
}

// Write пишет {error:{code, message_ru}} с нужным HTTP-статусом.
func Write(w http.ResponseWriter, status int, code, messageRu string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope{Error: errBody{Code: code, Message: messageRu}})
}
