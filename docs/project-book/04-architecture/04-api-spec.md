# API-спецификация (v1)

> Статус: `frozen v1.0 (2026-09-23)`. Связи: `02-functional/*`, `03-database-schema.md`.

База public: `api-public :8080/v1`. База admin: `api-admin :8081/v1/admin` (internal-only, доступ только `browser→web:3000/admin→server-side proxy→127.0.0.1:8081`). Auth: JWT cookie `taro_jwt` (user 30д) / `taro_admin` (12ч), `HttpOnly; Secure; SameSite=None + CSRF-header; Path=/`. Формат ошибок единый `{error:{code:LIMIT_EXCEEDED | INVALID_TG_HASH | VALIDATION | RATE_LIMITED | AI_TIMEOUT, message_ru}}`.

| Метод | Путь | Тело | Ответ | Ошибки |
|---|---|---|---|---|
| POST | `/v1/auth/telegram` | `{initData}` (HMAC bot_token) | `{user_id, is_new, trial_days}` | 401 INVALID_TG_HASH |
| POST | `/v1/auth/anon` | `{uuid, fingerprint?}` | `{user_id}` | 429 RATE_LIMITED |
| POST | `/v1/auth/link` | `{initData}` | `{merged:true}` | 409 ALREADY_LINKED |
| POST | `/v1/auth/refresh` | cookie | `{ok}` | 401 |
| POST | `/v1/auth/logout` | — | `{ok}` | — |
| GET | `/v1/me` | — | `{user_id, telegram_linked, age_confirmed, has_payment, valid_until, created_at, referral_code, readings_total, last_plan_code}` | 401 |
| POST | `/v1/auth/handoff` | `{plan_code}` | `{token, expires_in}` — **без `url`**: ссылку собирает клиент из `NEXT_PUBLIC_TG_APP_URL`, серверный URL = вектор фишинга | 404 при `auth.handoff_enabled=false`, 429 RATE_LIMITED (5/час/user + 20/час/IP), 503 при недоступном Redis |
| POST | `/v1/auth/link` | `{initData, fingerprint?, handoff?}` | `{merged, user_id, csrf_token}` | 422 нет initData/fp, 403 FP_MISMATCH, 409 ALREADY_LINKED/MERGE_CONFLICT, при `handoff`: 409 HANDOFF_EXPIRED (истёк/повтор/мусор) и 409 HANDOFF_IP_MISMATCH (сеть сменилась → клиент перевыдаёт токен молча) |
| GET | `/v1/spreads` | — | `[{code,name,positions,is_premium}]` | — (кэш 5м) |
| POST | `/v1/readings` | `{spread_code, question?}` + header `Idempotency-Key` (главный; поле в теле — fallback) + SSE `Accept` | SSE `tokens` → `{reading_id}`; повтор при `pending` возвращает тот же id с докачкой `GET` | 402 LIMIT_EXCEEDED, 422 VALIDATION |
| GET | `/v1/readings?limit&offset&q?` | `q` только premium | `[{id,spread,question,preview,created_at}]` | 401 |
| GET | `/v1/readings/:id` | — | `{..., cards[], interpretation}` | 404, 403 |
| GET | `/v1/entitlements/me` | — | `{plan, free_left, love_left_week, valid_until}` | — |
| POST | `/v1/payments/stars/invoice` | `{plan_code}` | `{invoice_link}` | 422 UNKNOWN_PLAN |
| POST | `/v1/payments/stars/webhook` | TG payload + `Secret-Token` | `{ok:true}` | 401 BAD_SIGN |
| POST | `/v1/payments/stars/verify` | `{provider_payment_id}` | `{status}` | 404 |
| GET | `/v1/referral/me` | — | `{code, invited, bonus_days}` | — |
| POST | `/v1/referral/apply` | `{code}` до 1-го reading | `{applied:pending}` | 409 SELF/ALREADY_REFERRED |
| GET | `/v1/admin/config` (8081) | — | `app_config + plans + spreads` | 403 |
| POST | `/v1/admin/config/publish` (8081) | `{diff}` | `{ok, invalidated}` | 422, audit |

Rate limits: `auth 20/мин/IP` + `anon 20/час/IP + капча`, `readings 10/мин/user`, `spreads 60/мин`, `admin 30/мин/admin_id` (не IP — за SSH все 127.0.0.1).

## Перенос покупки в Telegram (handoff)

Аноним заплатил, но покупка держится на строке `users`, найденной по `anon_uuid` в localStorage. Смена телефона или вход из Telegram = новый `users.id`, и человек не видит своей покупки. `link` требует `initData`, а он бывает только внутри Telegram, — то есть ровно тогда, когда перенос нужнее всего, способа не было.

1. `POST /v1/auth/handoff` — браузер просит одноразовый токен (Redis `handoff:<sha256>`, TTL 300с). Fail closed при недоступном Redis.
2. Клиент собирает `https://t.me/<bot>/<app>?startapp=pay_<token>` и уводит человека в Telegram. Ссылка только из `NEXT_PUBLIC_TG_APP_URL` (значение сборки), никогда из ответа сервера.
3. Mini App на `/handoff` достаёт токен из `initDataUnsafe.start_param` и шлёт `POST /v1/auth/link {initData, handoff}`. Повторная отправка заблокирована: токен одноразовый.
4. Сервер сверяет живую подпись `initData`, сверяет сеть (IPv4 `/24`, IPv6 `/64`), переносит `readings/subscriptions/payments/referrals/…` в Telegram-аккаунт одной транзакцией.

Что снято на этой ноге и почему: **fingerprint**. Он защищает от подделки `user_id` внутри Telegram; при переносе `user_id` приходит из одноразового токена, а устройства у браузера и WebApp разные по определению. Всё остальное (подпись `initData`, одноразовость, привязка к сети, advisory-локи, запрет реферальных циклов) остаётся.

**Проигравшая строка не удаляется, а переводится в `status='merged'`** (миграция 040, `anon_uuid`/`referral_code` освобождаются). Удаление было необратимым: пересланный из той же сети токен забрал бы покупку навсегда. Откат — `auth_handoffs` (кто, куда, когда, `survivor_tg_id`) плюс возврат строк вручную.

Остаточный риск: токен остаётся bearer-секретом, и пересылка **из той же /24** не блокируется — сеть, а не человек, проходит проверку. Отсюда обязательные предупреждение на экране, запись в `auth_handoffs`, уведомление браузеру и обратимость слияния.

Функция выключена по умолчанию: `app_config.auth.handoff_enabled=false`, и фронт молчит, пока пуст `NEXT_PUBLIC_TG_APP_URL`.
