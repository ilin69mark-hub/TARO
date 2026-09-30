// Перенос покупки из браузера в Telegram: одноразовый токен.
//
// Проблема. Человек платит в обычном браузере, потом открывает приложение в
// Telegram и не видит покупки. Причина физическая: WebView Telegram и браузер
// не делят cookie, поэтому у приложения нет сессии браузера, и существующая
// привязка (link.go) не на что опереться. Плюс fingerprint устройства у них
// разный, и link.go:168 отвечает fp_mismatch.
//
// Решение — тот же приём, что в OAuth: не «я это устройство», а «мне сервер
// выдал короткоживущий секрет для живой сессии». Отличие принципиальное:
// без токена нельзя, потому что передача user_id в ссылке — это полный захват
// аккаунта. Токен получает только владелец сессии, и он одноразовый.
//
// Чего токен НЕ решает. Он bearer-секрет, его можно переслать. Привязка к IP
// отсекает пересылку из другой сети, но не из той же (общий Wi-Fi). Поэтому
// безопасность переноса держится не на секретности, а на аудите (auth_handoffs,
// миграция 040) и на обратимости слияния.
//
// Где живёт токен. В Redis — как sess:/csrf: (session.go). Это не нарушает
// правило проекта «PG — источник правды»: потеря токена означает повторное
// нажатие, а не потерю денег. Срок 300 с, ключ — sha256 токена, сырое
// значение никогда не пишется ни в ключ, ни в лог.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/redis/go-redis/v9"
)

// Срок жизни токена. Пять минут — это «человек успел открыть Telegram и
// подтвердить оплату», с запасом на медленную сеть. Больше держать незачем:
// каждая минута жизни токена — это минута, в течение которой пересланная
// ссылка ещё работает.
const HandoffTTL = 300 * time.Second

// tokenBytes — 32 байта энтропии. Меньше нельзя: токенGuess перебором по
// сети возможен, и это единственный барьер для bearer-секрета.
const handoffTokenBytes = 32

// Ошибки переноса. Их различает фронт: по ним решает, просить ли новый токен
// молча (HANDOFF_IP_MISMATCH) или сказать человеку, что перенос невозможен.
var (
	ErrHandoffDisabled  = errors.New("handoff_disabled")
	ErrHandoffNotFound  = errors.New("handoff_not_found")
	ErrHandoffExpired   = errors.New("handoff_expired")
	ErrHandoffConsumed  = errors.New("handoff_consumed")
	ErrHandoffIPChanged = errors.New("handoff_ip_changed")
)

func handoffKey(token string) string { return "handoff:" + tokenHash(token) }

// tokenHash — ключ в Redis. Не сам токен: значение ключа попадает в вывод
// команды SCAN и в отчёты о состоянии Redis, а токен в этот момент ещё жив.
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newHandoffToken() (string, error) {
	b := make([]byte, handoffTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// base64url без паддинга: токен ходит в URL дип-линка, и '+'/'/' требуют
	// экранирования, а '=' ломает парсер query-строки на стороне Telegram.
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func validHandoffToken(token string) bool {
	if len(token) != base64.RawURLEncoding.EncodedLen(handoffTokenBytes) {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil
}

// handoffPayload — то, что лежит в Redis до момента потребления.
type handoffPayload struct {
	UserID   string `json:"user_id"`
	PlanCode string `json:"plan_code"`
	IPPrefix string `json:"ip_prefix"`
	IssuedAt int64  `json:"issued_at"`
}

// IPPrefix — сеть, а не адрес.
//
// Почему не полный адрес: у мобильных операторов адрес гуляет внутри одной
// подсети (CGNAT — сотни абонентов за одним адресом, смена вышки меняет
// последний октет). Привязка к точному адресу отказывала бы честному
// плательщику, который переключился с 4G на Wi-Fi по дороге к оплате.
//
// /24 для IPv4 и /64 для IPv6 — стандартные границы «одной сети» в смысле
// маршрутизации. Этого достаточно, чтобы отсечь пересылку из другого города
// или другой домашней сети, и при этом не ломать мобильный интернет.
func IPPrefix(ipStr string) string {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		// Адрес не распознан: привязываться не к чему. Возвращаем пустую
		// строку, и ConsumeHandoff откажет по ErrHandoffIPChanged, а не
		// по ошибке парсинга — это разные диагностируемые случаи.
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		masked := v4.Mask(net.CIDRMask(24, 32))
		return masked.String() + "/24"
	}
	masked := ip.Mask(net.CIDRMask(64, 128))
	return masked.String() + "/64"
}

// HandoffRequest — что просит браузер.
// Ответа с готовой ссылкой здесь нет намеренно. Серверный URL — это вектор
// фишинга: скомпрометированный ответ увёл бы человека с поддельной страницы на
// что угодно, и человек отдал бы токен переноса (а с ним покупку) именно туда.
// В проекте это правило уже зафиксировано для invoice_link (PaywallSheet,
// «Аудит B»). Ссылку собирает клиент из NEXT_PUBLIC_TG_APP_URL — это значение
// вшито на сборке и сервером не управляется.
type HandoffRequest struct {
	Token     string `json:"token"`
	ExpiresIn int    `json:"expires_in"`
}

// IssueHandoff — выдать токен на перенос. Требует живой сессии: caller
// передаёт user_id, уже проверенный RequireAuth.
func (s *Service) IssueHandoff(ctx context.Context, userID, planCode, ipStr string) (HandoffRequest, error) {
	if s.rd == nil {
		// Fail closed. Молча выдать перенос без хранилища нельзя: токен
		// получится бессрочным, и это ровно тот секрет, который нельзя
		// допускать в проде.
		return HandoffRequest{}, errors.New("redis unavailable")
	}
	prefix := IPPrefix(ipStr)
	if prefix == "" {
		return HandoffRequest{}, fmt.Errorf("bad ip")
	}
	token, err := newHandoffToken()
	if err != nil {
		return HandoffRequest{}, err
	}
	body, err := json.Marshal(handoffPayload{
		UserID:   userID,
		PlanCode: planCode,
		IPPrefix: prefix,
		IssuedAt: time.Now().Unix(),
	})
	if err != nil {
		return HandoffRequest{}, err
	}
	if err := s.rd.Set(ctx, handoffKey(token), body, HandoffTTL).Err(); err != nil {
		return HandoffRequest{}, err
	}
	return HandoffRequest{Token: token, ExpiresIn: int(HandoffTTL / time.Second)}, nil
}

// ConsumeHandoff — забрать токен и вернуть, кому он принадлежал.
//
// GetDel, а не Get+Del: две операции оставляют окно, в котором два
// параллельных запроса с одним токеном оба прочитают значение и оба запустят
// слияние. GetDel атомарен, и второй запрос честно получит ErrHandoffNotFound.
//
// Поглощение происходит ДО слияния. Если слияние затем упадёт, человек просто
// нажмёт ещё раз и получит новый токен; зато токен нельзя переиспользовать
// после неудачной попытки подобрать валидный initData.
func (s *Service) ConsumeHandoff(ctx context.Context, token, ipStr string) (userID string, planCode string, err error) {
	if s.rd == nil {
		return "", "", errors.New("redis unavailable")
	}
	if !validHandoffToken(token) {
		return "", "", ErrHandoffNotFound
	}
	raw, gerr := s.rd.GetDel(ctx, handoffKey(token)).Result()
	if gerr != nil {
		if gerr == redis.Nil {
			// Нет ключа = либо истёк, либо уже использован. Различать
			// бессмысленно и небезопасно: обе причины требуют от человека
			// одного и того же — нажать ещё раз.
			return "", "", ErrHandoffNotFound
		}
		return "", "", gerr
	}
	var p handoffPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return "", "", ErrHandoffNotFound
	}
	if !isUUID(p.UserID) {
		return "", "", ErrHandoffNotFound
	}
	// Привязка к сети. Несовпадение — самый частый «ложный» отказ: человек
	// переключился между сетями. Поэтому это отдельная ошибка, и фронт на неё
	// молча берёт новый токен, а не говорит «перенос запрещён».
	if IPPrefix(ipStr) != p.IPPrefix {
		return "", "", ErrHandoffIPChanged
	}
	return p.UserID, p.PlanCode, nil
}

// RecordHandoffConsumed — отметить в аудите, куда ушло слияние. Пишется в PG
// после успешного слияния: Redis к этому моменту уже очищен, а вопрос «мой
// аккаунт куда делся» без survivor_tg_id не закрывается.
func (s *Service) RecordHandoffConsumed(ctx context.Context, userID string, survivorTGID int64) error {
	_, err := s.pg.Exec(ctx, `
		UPDATE auth_handoffs SET consumed_at=now(), survivor_tg_id=$2
		WHERE user_id=$1 AND consumed_at IS NULL`, userID, survivorTGID)
	return err
}

// RecordHandoffIssued — след выдачи, до слияния. Нужен, чтобы по журналу было
// видно и «выдали, но не пришли» — это самая частая картина при разборе.
func (s *Service) RecordHandoffIssued(ctx context.Context, userID, planCode, ipPrefix string) error {
	_, err := s.pg.Exec(ctx,
		`INSERT INTO auth_handoffs (user_id, plan_code, ip_prefix) VALUES ($1,$2,$3)`,
		userID, planCode, ipPrefix)
	return err
}
