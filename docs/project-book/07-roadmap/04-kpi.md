# KPI по этапам

> Статус: `audit-fix C`. Связи: `02-month2-3-analytics-bot.md`.
| Этап | KPI |
|---|---|
| Нед. 7 (запуск 50 друзей) | 50 друзей, ≥80 чтений, 0×500 на paywall, LCP лендинга <2.5s + LCP 3D <4.0/<5.0s |
| Мес. 2 | 1000 reg, D1 ≥30%, D7 ≥15%, conv free→pay ≥2% |
| Мес. 3 | MRR ≥30k₽, churn <15%/мес, AI-cost <10% выручки |
| Мес. 6 | 1000 платных → решение о нативе; иначе — нет |

Формулы: `MRR = month_299×299 + year_2490/12` (single_99 — one-off, в MRR не входит; trial/referral-бонусы — не MRR); `churn = 1 − продлившие/active месяц назад` (без trial/referral); `AI-cost = OpenRouter$/выручка`. Источник — `subscriptions.price_rub_snapshot + payments.amount_rub`.

Воронка PostHog: `visit → spread_open{spread_code} → reading_done{reading_id} → paywall_show{plan} → pay_success{plan,amount_rub} + trial_start + delete_me` (серверный бэкап в PG на случай AdBlock). Разбор каждый понедельник 30 мин.
