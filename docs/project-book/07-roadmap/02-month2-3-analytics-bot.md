# Месяц 2–3: аналитика, итерации, бот

> Статус: `audit-fix C`. Связи: `04-kpi.md`.
- Аналитика: PostHog free + серверный бэкап в PG. События: `visit, spread_open{spread_code}, reading_done{reading_id}, paywall_show{plan}, pay_success{plan,amount_rub}, trial_start, delete_me` (+ `user_id/anon_id`). Predeploy-чек «PostHog пишет» — уже в нед.7, не в мес.2.
- Итерации: топ-1 боль из фидбека/нед (не 5 сразу).
- TG-бот: «карта дня» push в 21:00 (opt-in), ссылка на PWA.
- Выход: retention D7 ≥15%, pay-conversion ≥2%.
