// Сессии sess: + CSRF (см. 03-nonfunctional/02, 04-api-spec.md, T10).
// SameSite=None требует CSRF-защиты: все POST/PUT/DELETE /v1/* (кроме Stars-webhook
// с Secret-Token) обязаны нести непустой X-CSRF. Web шлет его всегда (см. web/lib/api.ts, T16).
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"taro/api/internal/apierr"
)

// sessKey — единый ключ сессии (см. 05-cache-redis.md). `refresh:` удален.
func sessKey(userID string) string { return "sess:" + userID }

func newSessHash() string {
	v, _ := newTokenID()
	return v
}

func (s *Service) rememberSession(ctx context.Context, userID string, ttl time.Duration, sessionID ...string) error {
	sid := newSessHash()
	if len(sessionID) > 0 && sessionID[0] != "" {
		sid = sessionID[0]
	}
	return s.rd.Set(ctx, sessKey(userID), sid, ttl).Err()
}

func (s *Service) issueSession(ctx context.Context, userID string, ttl time.Duration) (string, error) {
	sid, err := newTokenID()
	if err != nil {
		return "", err
	}
	tok, err := IssueJWT(userID, ttl, sid)
	if err != nil {
		return "", err
	}
	if err := s.rememberSession(ctx, userID, ttl, sid); err != nil {
		return "", err
	}
	return tok, nil
}

const rotateSessionLua = `
local current = redis.call('GET', KEYS[1])
if current ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
return 1
`

var errSessionRotated = fmt.Errorf("session_rotated")

func (s *Service) rotateSession(ctx context.Context, userID, oldSID string, ttl time.Duration) (string, error) {
	sid, err := newTokenID()
	if err != nil {
		return "", err
	}
	ok, err := s.rd.Eval(ctx, rotateSessionLua, []string{sessKey(userID)}, oldSID, sid, strconv.FormatInt(ttl.Milliseconds(), 10)).Int()
	if err != nil {
		return "", err
	}
	if ok != 1 {
		return "", errSessionRotated
	}
	return sid, nil
}

type sessionClaimsKey struct{}

func sessionClaims(ctx context.Context) (tokenClaims, bool) {
	claims, ok := ctx.Value(sessionClaimsKey{}).(tokenClaims)
	return claims, ok
}

func (s *Service) sessionMarkerOK(ctx context.Context, claims tokenClaims) (bool, error) {
	if s.rd == nil {
		return false, errors.New("redis unavailable")
	}
	marker, err := s.rd.Get(ctx, sessKey(claims.Subject)).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if marker == "" {
		return false, nil
	}
	if claims.Legacy && marker == "x" {
		return true, nil
	}
	if claims.SID == "" {
		return false, nil
	}
	return fingerprintsEqual(marker, claims.SID), nil
}

func ExpireAuthCookies(w http.ResponseWriter) {
	for _, name := range []string{CookieName, FpCookie, "taro_csrf"} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/", MaxAge: -1,
			HttpOnly: name != "taro_csrf", Secure: true, SameSite: http.SameSiteNoneMode,
		})
	}
	http.SetCookie(w, &http.Cookie{
		Name: "taro_admin", Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
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
		if c, cookieErr := r.Cookie(CookieName); cookieErr == nil {
			if claims, parseErr := parseJWT(c.Value); parseErr == nil {
				ok, markerErr := s.sessionMarkerOK(r.Context(), claims)
				if markerErr != nil {
					apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
					return
				}
				if !ok {
					ExpireAuthCookies(w)
					apierr.Write(w, http.StatusUnauthorized, apierr.CodeUnauthorized, "Сессия завершена, войди снова")
					return
				}
				want, regenerated, csrfErr := s.csrfForState(r.Context(), claims.Subject)
				if csrfErr != nil {
					apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
					return
				}
				if regenerated {
					setCSRFCookie(w, want)
				}
				if r.Header.Get("X-CSRF") == "" || r.Header.Get("X-CSRF") != want {
					apierr.Write(w, http.StatusForbidden, apierr.CodeForbidden, "Неверный CSRF-токен")
					return
				}
				next.ServeHTTP(w, r)
				return
			}
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
	v, _, err := s.csrfForState(ctx, uid)
	return v, err
}

const csrfRecoverLua = `
local current = redis.call('GET', KEYS[1])
if not current or current == '' then
  redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
  return 1
end
return 0
`

func (s *Service) csrfForState(ctx context.Context, uid string) (string, bool, error) {
	if s.rd == nil {
		return "", false, errors.New("redis unavailable")
	}
	key := "csrf:" + uid
	v, err := s.rd.Get(ctx, key).Result()
	if err == nil && v != "" {
		return v, false, nil
	}
	if err != nil && !errors.Is(err, redis.Nil) {
		return "", false, err
	}
	for range 2 {
		var b [32]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", false, err
		}
		value := hex.EncodeToString(b[:])
		created, err := s.rd.Eval(ctx, csrfRecoverLua, []string{key}, value, strconv.FormatInt(UserTTL.Milliseconds(), 10)).Int()
		if err != nil {
			return "", false, err
		}
		if created == 1 {
			return value, true, nil
		}
		v, err = s.rd.Get(ctx, key).Result()
		if err == nil && v != "" {
			return v, true, nil
		}
		if err != nil && !errors.Is(err, redis.Nil) {
			return "", false, err
		}
	}
	return "", false, errors.New("csrf token race")
}

// issueCSRF выдает токен в читаемую cookie taro_csrf + поле ответа (см. S07).
func (s *Service) issueCSRF(w http.ResponseWriter, ctx context.Context, uid string) (string, error) {
	v, _, err := s.csrfForState(ctx, uid)
	if err != nil {
		return "", err
	}
	setCSRFCookie(w, v)
	return v, nil
}

func setCSRFCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{
		Name: "taro_csrf", Value: value, Path: "/", MaxAge: int(UserTTL.Seconds()),
		Secure: true, SameSite: http.SameSiteNoneMode,
	})
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
	configured := strings.TrimSpace(os.Getenv("PUBLIC_ORIGIN"))
	if configured == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	want, err := url.Parse(configured)
	if err != nil || want.Host == "" {
		return false
	}
	return strings.EqualFold(u.Scheme, want.Scheme) && strings.EqualFold(u.Host, want.Host)
}
func (s *Service) HandleRefresh(w http.ResponseWriter, r *http.Request) {
	uid := UserID(r.Context())
	claims, ok := sessionClaims(r.Context())
	if uid == "" || !ok {
		ExpireAuthCookies(w)
		apierr.Write(w, http.StatusUnauthorized, apierr.CodeUnauthorized, "Нужен вход")
		return
	}
	var sid string
	var err error
	if claims.Legacy {
		ok, markerErr := s.sessionMarkerOK(r.Context(), claims)
		if markerErr != nil {
			apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
			return
		}
		if !ok {
			ExpireAuthCookies(w)
			apierr.Write(w, http.StatusUnauthorized, apierr.CodeUnauthorized, "Сессия завершена, войди снова")
			return
		}
		sid, err = newTokenID()
		if err == nil {
			err = s.rd.Set(r.Context(), sessKey(uid), sid, UserTTL).Err()
		}
	} else {
		sid, err = s.rotateSession(r.Context(), uid, claims.SID, UserTTL)
	}
	if err != nil {
		if err == errSessionRotated {
			ExpireAuthCookies(w)
			apierr.Write(w, http.StatusUnauthorized, apierr.CodeUnauthorized, "Сессия завершена, войди снова")
			return
		}
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
		return
	}
	tok, err := IssueJWT(uid, UserTTL, sid)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать сессию")
		return
	}
	writeCookie(w, tok, UserTTL)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (s *Service) HandleLogout(w http.ResponseWriter, r *http.Request) {
	uid := UserID(r.Context())
	if uid != "" {
		if s.rd == nil {
			apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
			return
		}
		if err := s.rd.Del(r.Context(), sessKey(uid), "csrf:"+uid).Err(); err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
			return
		}
	}
	ExpireAuthCookies(w)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
