# Free vs Paid + подписка (как конфиг)

> Статус: `frozen v1.0 (2026-09-23)`. Связи: `07-admin-config.md`, `04-architecture/03-database-schema.md` (`plans`, `entitlements`).

> Решение frozen: trial 3 дня за привязку TG — ДА (Must); разовый расклад 99₽ — оставляем в MVP (Must).
> E09-решение (frozen 2026-09-23): базовая цена 299₽/мес, A/B выключен (`ab.price_month.enabled=false`).

## Дефолты (меняются в админке без кода, источник — `app_config` + `plans`)
| Параметр | Дефолт | Где крутится |
|---|---|---|
| Free | 1 расклад/день (любой НЕ premium-спред) | `app_config.free.daily_limit=1` (не в `plans`!) |
| Love для free | 1/неделю (`love.is_premium=false`, но со своим счетчиком) | `app_config.love.free_weekly=1`, счетчик `entitlements.love_used_week` + Redis `ent:<user>:love:<ISO-week>` |
| Premium месяц | 299₽ / 30 дней (199 Stars), безлимит, история безлимит | `plans.code=month_299` |
| Premium год | 2490₽ / 365 дней (−30%) | `plans.code=year_2490` |
| Разовый расклад | 99₽ за 1 конкретный premium-расклад (привязка к платежу) | `plans.code=single_99` → `single_entitlements` |
| Trial | 3 дня premium за привязку TG, 1 раз, только `tg_id NOT NULL` | `app_config.trial.{enabled,days:3,require_tg:true}`, пишет в `subscriptions` как bonus-план |

## Логика проверки (бэк, единый `EntitlementsService`, порядок важен)
```
1. if active subscription (subscriptions.valid_until > now(), status=active) → allow  // покрывает оплату, trial, referral-бонусы
2. elif spread.code=='love' and love_used_week < free_weekly → allow (атомарный Lua INCR ent:<user>:love:<week>)
3. elif spread.is_premium and single_entitlements exists unused for (user, spread) → allow (пометить consumed_reading_id)
4. elif NOT spread.is_premium and daily Lua INCR ent:<user>:<date> <= daily_limit → allow
5. else → 402 {error:{code:LIMIT_EXCEEDED, paywall: активные plans}} (код единый везде!)
```
Атомарность: шаги 2 и 4 — Lua `INCR + EXPIREAT(00:00 MSK) + сравнение`, проигравший получает 402. Продление: `valid_until = max(now(), valid_until) + duration`. PG — источник правды; Redis — ускоритель (дропать `ent:*` запрещено, nightly `PG=MAX(PG,Redis)`).

## Paywall (мягкий)
- Текст из конфига, не хардкод. CTA: «Продолжить безлимитно — 299₽/мес».
- После оплаты: `INSERT payments … ON CONFLICT(provider_payment_id) DO NOTHING RETURNING` → начислять `valid_until` только если `inserted=true`, иначе вернуть текущий статус (защита от двойного webhook). Инвалидация точечная `DEL ent:<user_id>:<date>` (не `KEYS`).

## Edge
| Кейс | Поведение |
|---|---|
| Оплата упала посередине | `payments.status=pending` + job `expire pending>15м → expired` → webhook подтверждает → `succeeded`; иначе лимит не тратится |
| Чарджбэк/возврат | `payments.status=refunded` + `subscriptions.valid_until -= duration (min now)` + `status=revoked` + `admin_audit` |
| Смена цены в админке | новая строка `plans(code, valid_from=now)`; старые подписки/платежи хранят `price_rub_snapshot`, не трогаем |
| Накрутка free (N uuid) | см. antifraud: fingerprint + IP-лимит |
| Вопрос >500 символов | 500 Unicode code points NFC, иначе 422 VALIDATION |
