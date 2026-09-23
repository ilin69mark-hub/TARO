# Платежи: Stars №1 (обоснование)

> Статус: `draft`. Связи: `02-functional/05-entitlements-billing.md`.

## Варианты
| Способ | Плюсы | Минусы | Вердикт |
|---|---|---|---|
| **Telegram Stars** | 0% на вывод через TG, встроен в WebApp, аудитория уже в TG, без KYC на старте | только внутри TG (браузер — инструкция «открой в TG») | **№1 для MVP** |
| Boosty | RU-карты, подписки | комиссия 7–10%, редирект наружу, модерация эзотерики | №2 (мес. 3) |
| ЮKassa | карты/СБП, чеки | нужен ИП/самозанятость + KYC, дольше | №3 (когда >100 платных) |

## Флоу Stars (идемпотентный)
1. `POST /v1/payments/stars/invoice {plan_code}` → `createInvoiceLink (stars_amount из plans)` → отдаем `invoice_link`. Цены: 299₽ = 199 Stars (≈1.5₽/Star, округление в конфиге `plans.stars_amount`).
2. Юзер платит в TG → `POST /v1/payments/stars/webhook` (проверка `Secret-Token` header + IP TG, не путать с HMAC initData) → `INSERT payments … ON CONFLICT(provider_payment_id) DO NOTHING RETURNING` → начисляем `valid_until = max(now(),valid_until)+duration` только если `inserted=true`, иначе `200 {ok:true, duplicate:true}`. Ретраи TG безопасны.
3. `payments.status`: `pending` (invoice создан) → `succeeded` (webhook) / `expired` (job `expire pending>15м`) / `refunded` (возврат). Точечная инвалидация `DEL ent:<user>:<date>`.

## Edge
- Webhook не пришел: кнопка «Я оплатил» → `POST /v1/payments/stars/verify {provider_payment_id}` (сверка через Bot API, тот же `ON CONFLICT`).
- Возврат: ручной `refundStarPayment` → `payments.status=refunded` + `subscriptions.valid_until -= duration (min now)` + `status=revoked` + `admin_audit`.
- Браузер без TG: QR «открой в Telegram» + `pending` 15 мин (job чистит).

## Учет
Все суммы дублируем в `payments.amount_rub` для будущей ЮKassa без миграции.
