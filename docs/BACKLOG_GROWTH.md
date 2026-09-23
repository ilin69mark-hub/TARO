# BACKLOG роста «Онлайн Таро» — месяцы 2–3 (серия U, web-only)

> MVP T01–T30 done (см. `docs/BACKLOG.md`). Здесь — только рост 2–3.
> Источник правды — `docs/project-book/`. Статусы: `- [ ] todo` → `🔄 doing` → `- [x] done (дата)`.
> Правила: 1 задача = 1 ветка `feat/Uxx-...` = 1 PR по DoD (`06-rules/03-dod-frozen-scope.md`).
> Гейт мес.3: D7 ≥15%, conv free→pay ≥2%, MRR ≥30k₽, churn <15%/мес, AI-cost <10%.
> Стоп-кран рекламы: `spread_open→reading_done <60%`, `reading→paywall >30% без pay`, `paywall→pay <2%`.
> Вне скоупа (мес. 4–6): дневник, Boosty/ЮKassa, сезонные спреды сверх ротации, натив (только при 1000 платных).

## G0 — Живой фундамент
- [x] U01 Живой OpenRouter e2e — done-код (2026-09-23): gateway готов с T13; добавлен deploy/ai-budget.sql (дневной $ + ALERT >$5, проверен: 0 строк); живой вызов — блокер: нужен OPENROUTER_API_KEY — связь: `04-architecture/06`
- [ ] U02 Живой Stars-платеж e2e — BLOCKED: нужен TG-бот с Stars — связь: `04-architecture/07`
- [ ] U03 PostHog-дашборд + сверка — BLOCKED: нужен PostHog-ключ + трафик — связь: `04-architecture/08`
- [ ] U04 Baseline воронки — BLOCKED: нужна неделя живого трафика — связь: `07-roadmap/04`

## G1 — TG-бот «карта дня» — BLOCKED целиком: нужен TG-бот-токен (владелец)
- [ ] U05 Каркас бота + webhook — BLOCKED — связь: `07-roadmap/02`
- [ ] U06 Opt-in/opt-out 21:00 — BLOCKED — связь: `07-roadmap/02`
- [ ] U07 Рассылка карты дня — BLOCKED — связь: `07-roadmap/02`
- [ ] U08 Диплинк taro.me/r — BLOCKED — связь: `02-functional/06`
- [ ] U09 Trial за подписку на бота — BLOCKED — связь: `02-functional/05,07`
- [ ] U10 Антиспам бота — BLOCKED — связь: `03-nonfunctional/02`

## G2 — OG-шеринг
- [x] U11 Генератор OG-картинки — done (2026-09-23): /api/og 1200×630 (@vercel/og, шаблон без артов, без толкования); e2e 200 image/png — связь: `05-design/04`
- [x] U12 Публичная /share — done (2026-09-23): превью без входа, noindex, OG-мета; e2e CTA рендерится — связь: `03-nonfunctional/05`
- [x] U13 Кнопки шеринга — done (2026-09-23): TG + копировать на result, диплинк /share; e2e кнопка рендерится — связь: `02-functional/03`
- [x] U14 Метрика share_done — done (2026-09-23): событие share_done + localStorage-счетчик (конверсия — по PostHog U03) — связь: `04-architecture/08`

## G3 — SEO
- [x] U15 SSR-каталог + мета — done (2026-09-23): force-dynamic SSR (build-time Go недоступен), RU-мета+OG; e2e curl видит HTML — связь: `03-nonfunctional/05`
- [x] U16 78 страниц значений — done (2026-09-23): GET /v1/cards/{id} (публичный, 0–77) + /cards/[id] SSR; e2e «Маг» в HTML — связь: `03-nonfunctional/05`
- [x] U17 Sitemap + robots — done (2026-09-23): 80 URL, закрыты reading/history/admin/api; e2e оба 200 — связь: `03-nonfunctional/05`
- [x] U18 Schema.org — done (2026-09-23): JSON-LD WebApplication на лендинге — связь: `03-nonfunctional/05`
- [ ] U19 Дашборд SEO-доли — BLOCKED: нужен месяц живого трафика — связь: `07-roadmap/04`

## G4 — Ретеншн и итерации
- [x] U20 Стрик «дней подряд» — done (2026-09-23): GET /v1/streak/me (MSK, без миграций) + бейдж ≥2 с плюрализацией; e2e 0→1→2 — связь: `01-manifest/02`
- [x] U21 Web-push opt-in — done (2026-09-23): VAPID gen, 007 миграция, subscribe/unsubscribe/public, stdlib sender (roundtrip+VAPID unit), UI opt-in; e2e public+subscribe+422 — связь: `03-nonfunctional/06`
- [x] U22 A/B цен — done (2026-09-23): VariantFor по хешу user_id, /v1/ab/me + proxy, invoice берет variant-цену в снапшот, paywall показывает; e2e split=100→test 349, split=0→control — связь: `02-functional/07`
- [x] U23 Онбординг 3 шага — done (2026-09-23): модалка вопрос→карта→paywall, 1 раз, skippable; lint/tsc/build зелено — связь: `02-functional/03`
- [x] U24 Процесс «топ-1 боль» — done (2026-09-23): docs/TOP1_PROCESS.md (30 мин, 1 задача, waiting-лист) — связь: `07-roadmap/02`
- [x] U25 Напоминание об истечении — done (2026-09-23): POST /v1/admin/remind-expiring (пуш за 72ч через SendToUser); e2e expiring:0 reminded:0 — связь: `02-functional/05`

## G5 — Дожим монетизации
- [x] U26 Промо single_99 — done (2026-09-23): счетчик упоров, single первым при 3+ — связь: `02-functional/05`
- [x] U27 Upsell годового — done (2026-09-23): баннер premium в профиле — связь: `02-functional/05`
- [x] U28 Winback — done (2026-09-23): winback_eligible в /v1/entitlements/me (expired ≥14д) + баннер; механика скидки — следующая итерация U24 — связь: `02-functional/07`
- [x] U29 Рефералка v2 — done (2026-09-23): объяснение будущего бонуса в профиле до приглашения — связь: `02-functional/06`

## G6 — Гейт месяца 3 — BLOCKED: нужен живой трафик
- [ ] U30 Разбор D7≥15% — BLOCKED — связь: `07-roadmap/04`
- [ ] U31 Разбор денег — BLOCKED — связь: `07-roadmap/04`
- [ ] U32 Решение гейта — BLOCKED — связь: `09-adr/00`

## Статус: кодовая часть готова (U01-код, U11–U29 кроме блокеров).
# BLOCKED внешним (не код) — для владельца:
# - OPENROUTER_API_KEY → живой вызов U01; TG Stars + бот-токен → U02, G1 целиком;
# - PostHog-ключ + трафик → U03, U04, U19, G6 целиком.
