// Сессии sess: + CSRF (см. 03-nonfunctional/02, 04-api-spec.md, T10).
// SameSite=None требует CSRF-защиты: все POST/PUT/DELETE /v1/* (кроме Stars-webhook
// с Secret-Token) обязаны нести непустой X-CSRF. Web шлет его всегда (см. web/lib/api.ts, T16).
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

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

// RequireCSRF — middleware (см. S07): per-session токен + Origin-check.
// POST/PUT/DELETE (кроме Stars-webhook с Secret-Token):
//   - валидный JWT → X-CSRF обязан равняться csrf:<uid> из Redis;
//   - без JWT (входы) → проверяем Origin/Referer при наличии (curl без Origin пропускаем — держат лимиты).
//
// Прокси токен НЕ инжектит (см. web/lib/api.ts) — шлет клиентский из cookie taro_csrf.
// RequireCSRF — метод (нужен Redis, см. S07).
func (s *Service) RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/payments/stars/webhook" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodDelete {
			next.ServeHTTP(w, r)
			return
		}
		if uid := jwtUser(r); uid != "" {
			want, err := s.csrfFor(r.Context(), uid)
			if err != nil || r.Header.Get("X-CSRF") == "" || r.Header.Get("X-CSRF") != want {
				apierr.Write(w, http.StatusForbidden, apierr.CodeForbidden, "Неверный CSRF-токен")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if !originOK(r) {
			apierr.Write(w, http.StatusForbidden, apierr.CodeForbidden, "Неверный Origin")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// jwtUser парсит user_id из cookie без проверки sess (для CSRF-гейта).
func jwtUser(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	uid, err := ParseJWT(c.Value)
	if err != nil {
		return ""
	}
	return uid
}

// csrfFor возвращает per-session CSRF-токен (создает при отсутствии, TTL=сессии).
func (s *Service) csrfFor(ctx context.Context, uid string) (string, error) {
	if v, err := s.rd.Get(ctx, "csrf:"+uid).Result(); err == nil && v != "" {
		return v, nil
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	v := hex.EncodeToString(b[:])
	if err := s.rd.Set(ctx, "csrf:"+uid, v, UserTTL).Err(); err != nil {
		return "", err
	}
	return v, nil
}

// issueCSRF выдает токен в читаемую cookie taro_csrf + поле ответа (см. S07).
func (s *Service) issueCSRF(w http.ResponseWriter, ctx context.Context, uid string) string {
	v, err := s.csrfFor(ctx, uid)
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{
		Name: "taro_csrf", Value: v, Path: "/", MaxAge: int(UserTTL.Seconds()),
		Secure: true, SameSite: http.SameSiteNoneMode,
	})
	return v
}

// originOK сверяет Origin/Referer с хостом запроса (пустые — пропускаем: curl/тесты).
func originOK(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	want := r.Host
	// за nginx хост приходит в X-Forwarded-Host/Host — сравниваем как есть
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		want = strings.Split(h, ",")[0]
	}
	return strings.EqualFold(strings.TrimSpace(u.Host), strings.TrimSpace(want))
}

// HandleRefresh — POST /v1/auth/refresh: ротация sess + новый cookie. Нужен валидный JWT.
// S03: ошибка Redis → 503 (не 500 — сессия жива, хранилище лежит).
func (s *Service) HandleRefresh(w http.ResponseWriter, r *http.Request) {
	uid := UserID(r.Context())
	if uid == "" {
		apierr.Write(w, http.StatusUnauthorized, apierr.CodeUnauthorized, "Нужен вход")
		return
	}
	if err := s.rd.Set(r.Context(), sessKey(uid), newSessHash(), UserTTL).Err(); err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
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
