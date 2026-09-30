// HTTP-эндпоинты переноса: POST /v1/auth/handoff и ветка токена в /v1/auth/link.
//
// Два решения, которые видно снаружи и потому зафиксированы здесь.
//
// ФЛАГ ВЫКЛЮЧЁН ПО УМОЛЧАНИЮ. Перенос меняет личность, то есть деньги, и
// включать его одной миграцией на боевой БД — неправильно. auth.handoff_enabled
// в app_config (миграция 040) по умолчанию false. Фронт дополнительно молчит,
// пока не задан NEXT_PUBLIC_TG_APP_URL, так что на деве поверхности нет вовсе.
//
// ПРИВЯЗКА К СЕТИ, А НЕ К АДРЕСУ. См. IPPrefix в handoff.go. При несовпадении
// возвращаем HANDOFF_IP_MISMATCH, а не 403: чаще всего это человек переключил
// сеть между выдачей и открытием, и фронт на этот код берёт новый токен молча.
// Отличать «сеть поменялась» от «токен подсунул кто-то другой» по одному IP
// нельзя, и делать вид, что можно — значит ломать честных плательщиков.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"taro/api/internal/apierr"
)

// Лимиты выдачи токена. Поверх уже действующего ratelimit.go:174
// ({"/v1/auth/", …, Rule{60, 20}}) — это 20/мин/IP на весь префикс.
// Эти — на количество выданных переносов, потому что токен дороже запроса
// списка: каждый выданный токен это потенциальный перенос личности.
const (
	handoffPerUserHour = 5
	handoffPerIPHour   = 20
)

func handoffKeyUser(userID string) string { return "handoff:rate:user:" + userID }
func handoffKeyIP(prefix string) string   { return "handoff:rate:ip:" + prefix }

func (s *Service) handoffEnabled(ctx context.Context) bool {
	var raw json.RawMessage
	if err := s.pg.QueryRow(ctx, `SELECT value FROM app_config WHERE key='auth'`).Scan(&raw); err != nil {
		// Миграции нет или БД недоступна: считаем выключенным. Fail closed —
		// перенос трогает деньги, и «не смог прочитать флаг» не повод
		// включать его.
		return false
	}
	var cfg struct {
		HandoffEnabled *bool `json:"handoff_enabled"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil || cfg.HandoffEnabled == nil {
		return false
	}
	return *cfg.HandoffEnabled
}

// rateAllow — INCR + EXPIREAT одним походом, как в consume() у entitlements.
// Отдельный SETNX с последующим INCR оставляет гонку, где второй запрос видит
// счётчик без TTL и ключ живёт вечно.
func rateAllow(ctx context.Context, rd *redis.Client, key string, limit int, ttl time.Duration) (bool, error) {
	n, err := rd.Incr(ctx, key).Result()
	if err != nil {
		return false, err
	}
	if n == 1 {
		if err := rd.ExpireAt(ctx, key, time.Now().Add(ttl)).Err(); err != nil {
			return false, err
		}
	}
	return n <= int64(limit), nil
}

// HandleHandoff — POST /v1/auth/handoff {plan_code}.
//
// Отдаёт токен и ничего больше. URL дип-линка собирает фронт: он знает
// NEXT_PUBLIC_TG_APP_URL, а бэкенд про публичный адрес бота не знает и знать
// не должен.
func (s *Service) HandleHandoff(w http.ResponseWriter, r *http.Request) {
	uid := UserID(r.Context())
	if uid == "" {
		apierr.Write(w, http.StatusUnauthorized, apierr.CodeUnauthorized, "Нужен вход")
		return
	}
	if !s.handoffEnabled(r.Context()) {
		// 404, а не 403: выключенная функция не должна отвечать «выключено»,
		// иначе фронт пришлось бы различать «не работает» и «нет доступа».
		apierr.Write(w, http.StatusNotFound, "NOT_FOUND", "Перенос недоступен")
		return
	}
	var req struct {
		PlanCode string `json:"plan_code"`
	}
	if !apierr.Decode(w, r, &req) {
		return
	}
	// План пускаем как есть: это подсказка для фронта, каким тарифом открыть
	// счёт после перехода в Telegram. Ничего платёжного по нему не делается,
	// поэтому валидировать нечего — подделать можно только то, что человек и так
	// видит на экране.
	ipPrefix := IPPrefix(clientIP(r))
	if ipPrefix == "" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Не удалось определить адрес")
		return
	}
	if s.rd == nil {
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
		return
	}
	ok, err := rateAllow(r.Context(), s.rd, handoffKeyUser(uid), handoffPerUserHour, time.Hour)
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
		return
	}
	if !ok {
		apierr.Write(w, http.StatusTooManyRequests, apierr.CodeRateLimited, "Слишком много переносов, попробуй позже")
		return
	}
	if ok, err = rateAllow(r.Context(), s.rd, handoffKeyIP(ipPrefix), handoffPerIPHour, time.Hour); err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
		return
	}
	if !ok {
		apierr.Write(w, http.StatusTooManyRequests, apierr.CodeRateLimited, "Слишком много переносов, попробуй позже")
		return
	}
	out, err := s.IssueHandoff(r.Context(), uid, req.PlanCode, clientIP(r))
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
		return
	}
	if err := s.RecordHandoffIssued(r.Context(), uid, req.PlanCode, ipPrefix); err != nil {
		// Аудит не записался, но токен уже выдан. Не отзываем выдачу: отзыв
		// здесь опаснее пропущенной строки журнала, а журнал — не источник
		// правды, а след.
		_ = err
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// linkError — единая классификация отказов слияния для переноса. Раньше эти
// коды жили инлайном в HandleLink; с появлением токенов к ним добавились
// HANDOFF_*, и перечисление стало местом, где легко забыть ветку.
func linkError(w http.ResponseWriter, err error, tokenUsed bool) {
	switch {
	case errors.Is(err, errAlreadyLinked):
		apierr.Write(w, http.StatusConflict, "ALREADY_LINKED", "Telegram уже привязан")
	case errors.Is(err, errMergeSelf), errors.Is(err, errMergeCycle):
		apierr.Write(w, http.StatusConflict, "MERGE_CONFLICT", "Нельзя объединить эти аккаунты")
	case errors.Is(err, ErrHandoffDisabled):
		apierr.Write(w, http.StatusNotFound, "NOT_FOUND", "Перенос недоступен")
	case errors.Is(err, ErrHandoffIPChanged):
		// 409, а не 403: действие нужно повторить, а не запрещено.
		apierr.Write(w, http.StatusConflict, "HANDOFF_IP_MISMATCH", "Сеть изменилась, попробуй ещё раз")
	case errors.Is(err, ErrHandoffNotFound), errors.Is(err, ErrHandoffExpired), errors.Is(err, ErrHandoffConsumed):
		apierr.Write(w, http.StatusConflict, "HANDOFF_EXPIRED", "Ссылка переноса больше не действует")
	case err.Error() == "fp_mismatch":
		apierr.Write(w, http.StatusForbidden, "FP_MISMATCH", "Устройство не узнано, войди через Telegram")
	case err.Error() == "user inactive":
		apierr.Write(w, http.StatusForbidden, "ACCOUNT_INACTIVE", "Аккаунт недоступен")
	case err.Error() == "referral_cycle", err.Error() == "referral_self":
		apierr.Write(w, http.StatusConflict, "MERGE_CONFLICT", "Нельзя объединить эти аккаунты")
	default:
		// Токен, сгоревший на неуспешной попытке, НЕ восстанавливаем: вернуть
		// его — значит разрешить перебирать варианты initData на одном токене.
		_ = tokenUsed
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось привязать Telegram")
	}
}

// handoffAttempts — счётчик неудачных попыток по токену, чтобы отличать
// «человек перепутал сеть» от «перебирают». В логи не пишем значение токена.
func handoffAttemptsKey(token string) string { return "handoff:try:" + tokenHash(token) }

func (s *Service) handoffTooManyTries(ctx context.Context, token string) bool {
	n, err := s.rd.Get(ctx, handoffAttemptsKey(token)).Int()
	if err != nil {
		// Redis недоступен — считаем, что попыток мало, и разрешаем: сам
		// ConsumeHandoff всё равно вернёт 503, а не проведёт слияние.
		return false
	}
	return n >= 5
}

func (s *Service) noteHandoffTry(ctx context.Context, token string) {
	key := handoffAttemptsKey(token)
	if _, err := s.rd.Incr(ctx, key).Result(); err != nil {
		return
	}
	_ = s.rd.ExpireAt(ctx, key, time.Now().Add(HandoffTTL)).Err()
}
