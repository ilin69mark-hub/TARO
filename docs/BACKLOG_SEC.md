# BACKLOG Security «Онлайн Таро» — серия S (сторонний аудит 2026-09-23)

> Источник — свежий security-аудит (без авторской привязанности). Только безопасность/надёжность/корректность.
> Статусы: `- [ ] todo` → `🔄 doing` → `- [x] done (дата)` + e2e-подтверждение.
> Правило: 1 задача = 1 ветка `feat/Sxx-...` = 1 PR. После серии — повторный аудит топ-10.
> Детали закрытых — в git-истории (`BACKLOG_SEC.md` до чистки 2026-09-23).

## Итоги (S01–S10 done 2026-09-23)
- **Критика:** fail-closed пустого TG-токена + обязательный auth_date; SSRF-allowlist push; PG-fallback лимитов + 503; кэш с хешем вопроса + delimiters + NUL→422; webhook Rollback+500 и честный refund.
- **Харденинг:** `apierr.Decode` (1MB+strict) в 17 хендлерах, recover в горутинах, дедлайны 25/30с; per-session CSRF + Origin-check; UUID-валидация + fingerprint; USER/tzdata/HEALTHCHECK, VAPID в `.env`, CI security-job.
- **Инфра:** nginx headers/SSE/лимиты/deny-8081, cron с `X-Admin-Token`, честный deploy (concurrency, gate 18:00, fail на публичном 8081).

## Открытые по этой серии
_Нет — все S01–S10 закрыты. Повторный аудит топ-10 — по запросу. ВНИМАНИЕ: пересборка образов ждет registry — `compose build` при восстановлении!_
