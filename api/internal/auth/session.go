// Сессии sess: + CSRF (см. 03-nonfunctional/02, 04-api-spec.md, T10).
// SameSite=None требует CSRF-защиты: все POST/PUT/DELETE /v1/* (кроме Stars-webhook
// с Secret-Token) обязаны нести непустой X-CSRF. Web шлет его всегда (см. web/lib/api.ts, T16).
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"context"

	"taro/api/internal/apierr"
)

// sessKey — единый ключ сессии (см. 05-cache-redis.md). `refresh:` удален.
func sessKey(userID string) string { return "sess:" + userID }

// newSessHash — случайный маркер сессии для rotation.
func newSessHash() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// rememberSession пишет sess:<uid> на TTL токена (вызывать при каждом login/link).
func (s *Service) rememberSession(ctx context.Context, userID string, ttl time.Duration) error {
	return s.rd.Set(ctx, sessKey(userID), newSessHash(), ttl).Err()
}

// RequireCSRF — middleware: POST/PUT/DELETE требуют X-CSRF.
// Исключение: Stars-webhook (у него свой Secret-Token, см. T29).
func RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/payments/stars/webhook" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
			if r.Header.Get("X-CSRF") == "" {
				apierr.Write(w, http.StatusForbidden, apierr.CodeForbidden, "Нужен X-CSRF заголовок")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// HandleRefresh — POST /v1/auth/refresh: ротация sess + новый cookie. Нужен валидный JWT.
func (s *Service) HandleRefresh(w http.ResponseWriter, r *http.Request) {
	uid := UserID(r.Context())
	if uid == "" {
		apierr.Write(w, http.StatusUnauthorized, apierr.CodeUnauthorized, "Нужен вход")
		return
	}
	if err := s.rd.Set(r.Context(), sessKey(uid), newSessHash(), UserTTL).Err(); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось обновить сессию")
		return
	}
	tok, err := IssueJWT(uid, UserTTL)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать сессию")
		return
	}
	writeCookie(w, tok, UserTTL)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// HandleLogout — POST /v1/auth/logout: DEL sess + гашение cookie.
func (s *Service) HandleLogout(w http.ResponseWriter, r *http.Request) {
	uid := UserID(r.Context())
	if uid != "" {
		_ = s.rd.Del(r.Context(), sessKey(uid)).Err()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
