// Package apierr — единый формат ошибок (см. docs/project-book/04-architecture/02).
package apierr

import (
	"encoding/json"
	"net/http"
	"strings"
)

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
	CodeUnavailable  = "UNAVAILABLE" // 503: зависимость (Redis/AI) недоступна, ретрай позже (см. S03)
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

// MaxBody — единый лимит тела запроса 1MB (аудит C: отдельных 32KB для auth нет —
// старый комментарий врал; при нужде ввести MaxBodyAuth отдельно в auth-хендлерах).
const MaxBody = 1 << 20

// Decode читает JSON-тело с лимитом и строгим режимом (без unknown-полей).
// Возвращает false + пишет 413/422 при переполнении/битом JSON.
func Decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			Write(w, http.StatusRequestEntityTooLarge, CodeValidation, "Слишком большое тело")
		} else {
			Write(w, http.StatusUnprocessableEntity, CodeValidation, "Некорректное тело")
		}
		return false
	}
	return true
}

// Recover — recover для горутин (см. S06): паника гасится, процесс жив.
// Использование: go func() { defer apierr.Recover(); ... }().
func Recover() {
	_ = recover()
}
