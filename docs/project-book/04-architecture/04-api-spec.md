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
