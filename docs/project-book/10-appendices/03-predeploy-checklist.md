# Чек-лист перед деплоем

> Статус: `frozen v1.0`.
- [ ] `migrate up` на стейдже зелено, seed 78 карт + 5 spreads + 4 plans (free/month_299/year_2490/single_99)
- [ ] trial 3 дня за TG-Link проверен (повторно не выдается)
- [ ] single_99 покупает 1 premium-расклад без подписки
- [ ] `lighthouse` LCP лендинга <2.5s + LCP 3D <4.0/<5.0s (fail если mobile >5s), PWA installable
- [ ] AI fallback проверен (выкл. ключ → значения из БД)
- [ ] 402 paywall не 500, цены из конфига
- [ ] Stars webhook подпись ок, `pending>15м` алерт
- [ ] `.env` не в git, секреты в VPS env
- [ ] `:8081 (admin-api)` слушает только 127.0.0.1 — проверка `ss -tlnp`, снаружи закрыт (nmap/curl извне → timeout)
- [ ] бэкап PG daily, деплой до 18:00 МСК
