# BACKLOG Security «Онлайн Таро» — серия S (сторонний аудит 2026-09-23)

> Источник — свежий security-аудит (без авторской привязанности). Только безопасность/надёжность/корректность.
> Статусы: `- [ ] todo` → `🔄 doing` → `- [x] done (дата)` + e2e-подтверждение.
> Правило: 1 задача = 1 ветка `feat/Sxx-...` = 1 PR. После серии — повторный аудит топ-10.

## S-критика (деньги/PII/outage)
- [x] S01 Fail-closed пустой TG_BOT_TOKEN — done (2026-09-23): отказ без TG_ALLOW_EMPTY=1 + auth_date обязателен и свеж; unit + e2e зелено — связь: `auth.go:51`
- [x] S02 SSRF-allowlist push endpoint — done (2026-09-23): https-only, без userinfo, блок private/loopback/link-local + DNS-ответ; e2e 4 кейса 422 + example.com ok — связь: `push.go`
- [x] S03 Redis fail-open/503 — done (2026-09-23): PG-fallback (SELECT FOR UPDATE), MSK-ключ, love TTL до понедельника, 503 вместо 401/500, ratelimit fail-open; e2e — связь: `entitlements/auth/ratelimit`
- [x] S04 AI-кэш question_hash + NUL-фильтр + пустой-ответ — done (2026-09-23): ключ с хешем вопроса, delimiters+анти-инструкция, NUL→422, пустой→failed без кэша; e2e — связь: `ai/readings/diary`
- [x] S05 Webhook Exec-check + честный refund — done (2026-09-23): ошибка вставки → Rollback+500; refund 502 без порчи + note (проверено в D3) — связь: `payments.go`

## S-харденинг
- [x] S06 recover + MaxBytesReader + WithTimeout — done (2026-09-23): apierr.Decode (1MB+strict) во всех 17 хендлерах, recover в 5 горутинах, дедлайны 25с SSE/30с worker; сюита зелена — связь: все пакеты
- [x] S07 Реальный CSRF — done (2026-09-23): per-session csrf:<uid> + cookie + JSON, форвард (не инжект), Origin-check; e2e fake→403/real→200/evil→403 — связь: `session.go/api.ts`
- [x] S08 Anon hardening — done (2026-09-23): isUUID без deps (422), fp-привязка (чужой → 403), web шлет taro_fp; e2e — связь: `auth.go/auth.ts`
- [x] S09 Docker/композ — done-код (2026-09-23): USER app/node, tzdata, HEALTHCHECK, EXPOSE 8081 убран, VAPID в .env (gitignored) + плейсхолдеры, CI security-job; сюита зелена. ВНИМАНИЕ: пересборка образов ждет registry (TLS down) — `compose build` при восстановлении! — связь: `Dockerfile*/compose/ci`
- [x] S10 Nginx/deploy — done (2026-09-23): headers, SSE buffering-off+timeouts, зоны payments/diary/api, deny-8081 блок, XFF/Proto, cron с X-Admin-Token (e2e 403/422/200+audit-NULL), deploy честный (concurrency, gate 18:00, health, fail на публичном 8081); nginx syntax ok + headers live; TLS — VPS/E10 — связь: `nginx/deploy`

## Статус: открыт (S01–S10 todo). Порядок — сверху вниз.
