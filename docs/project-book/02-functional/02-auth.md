# Auth: Telegram ID + UUID anon

> Статус: `draft`. Связи: `04-architecture/04-api-spec.md`, `03-nonfunctional/02-security-antifraud.md`.

## User Story
Как пользователь, я хочу войти за 5 секунд без пароля, чтобы сразу вытянуть карту.

## Флоу
1. WebApp открыт в Telegram → `window.Telegram.WebApp.initData` → `POST /v1/auth/telegram` → JWT в httpOnly cookie + `user_id`.
2. Открыт в браузере → `localStorage uuid` (генерируется) → `POST /v1/auth/anon {uuid}` → тот же JWT. Привязка TG позже одной кнопкой `Link` → `POST /v1/auth/link`.
3. JWT: user 30 дней / admin 12ч, refresh rotation в Redis (единый ключ `sess:<user_id>`, `refresh:` удален). Обновление — `POST /v1/auth/refresh`, выход — `POST /v1/auth/logout` (`DEL sess:*`).

## Merge anon→TG (транзакция, одноразовый trial)
`POST /v1/auth/link` в одной PG-транзакции: survivor = TG-аккаунт (или старший при TG→TG запрещен → 409). Правила: `valid_until = max(a,b)` (единственный источник — `subscriptions`), `trial` выдается 1 раз на связку `tg_id+fingerprint` (повторный Link триал не дает), `free_used/love_used = max(a,b)`, все `readings/payments/referrals/single_entitlements` перелинковываются на survivor, дубль `users` удаляется, второй JWT инвалидируется (`DEL sess:loser`).

## Acceptance Criteria (GWT)
- Given валидный `initData`, When POST telegram, Then 200 + `is_new` + JWT.
- Given поддельный `initData` (неверный hash), When POST, Then 401 `INVALID_TG_HASH`.
- Given anon uuid, When второй девайс с тем же uuid, Then тот же user (лимит 3 IP/сутки, иначе 429).

## Edge cases
| Кейс | Поведение |
|---|---|
| Сброс localStorage | новый uuid → новый anon; старый можно склеить через TG Link |
| Бот-ферма (100 uuid с 1 IP) | rate limit 20 reg/час/IP + fingerprint + капча на 21-й |
| TG недоступен | fallback anon, баннер «привяжи TG позже» |
| Logout | `POST /v1/auth/logout` чистит cookie + `DEL sess:<user_id>`, uuid в браузере остается |

## Единые лимиты (источник — `04-architecture/04-api-spec.md`)
`POST /v1/auth/* 20/мин/IP` (nginx+mw) + дополнительно `POST /v1/auth/anon 20/час/IP + капча с 21-й`; спор `3 IP/сутки` удален как неисполнимый.

## Конфиг (админка)
- `auth.jwt_ttl_days` (дефолт 30), `auth.anon_per_ip_per_hour` (дефолт 20) — в `07-admin-config.md`.
