# Аналитика: события + PostHog + серверный бэкап (T07)

> Статус: `audit-fix C + T07 done (2026-09-23)`. Связи: `07-roadmap/02-month2-3-analytics-bot.md`, `07-roadmap/04-kpi.md`, `10-appendices/03-predeploy-checklist.md`.
> Predeploy-чек «PostHog пишет» — уже в нед.7, не в мес.2.

## 1. События (имена заморожены — менять только через ADR)
| Событие | Когда | Props |
|---|---|---|
| `visit` | открытие `/` | `anon_id, tg (bool)` |
| `spread_open` | открыт каталог/спред | `spread_code` |
| `reading_done` | толкование доставлено | `reading_id, spread_code, free (bool)` |
| `paywall_show` | показан paywall | `plan, free_left` |
| `pay_success` | webhook succeeded | `plan, amount_rub, stars` |
| `trial_start` | выдан trial 3д | `user_id` |
| `delete_me` | запрос удаления | `user_id` |

Все события несут `user_id/anon_id`. Никаких сырых вопросов и толкований в пропсах (PII).

## 2. Клиент (PostHog free)
- Инициализация в `web/lib/analytics.ts`: только если задан `NEXT_PUBLIC_POSTHOG_KEY`, иначе no-op (dev тихий).
- Отправка: `visit, spread_open, reading_done(client), paywall_show`. AdBlock режет — не чиним на клиенте, бэкап на сервере.

## 3. Серверный бэкап в PG (источник для KPI при AdBlock)
- `reading_done` → строка `readings(status=done)` уже есть.
- `pay_success` → `payments(status=succeeded)` + `amount_rub` уже есть.
- `trial_start` → `subscriptions(plan_code=trial_3d)` + `admin_audit` уже есть.
- Формулы KPI (см. `07-roadmap/04-kpi.md`): `MRR` и `churn` считаются SQL по `subscriptions.price_rub_snapshot + payments`, без PostHog.

## 4. Воронка и стоп-кран (см. `10-appendices/04-ads-launch-checklist.md`)
`visit → spread_open → reading_done → paywall_show → pay_success`. Стоп рекламы: `spread_open→reading_done <60%`, `reading→paywall >30% без pay`, `paywall→pay <2%`.

## 5. DoD аналитики (нед.7)
- [ ] PostHog пишет 7 событий (проверено в dev с ключом)
- [ ] KPI-SQL дает те же цифры, что воронка (±5%)
- [ ] В пропсах нет PII (ревью PR grep `question|interpretation`)
