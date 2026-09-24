# BACKLOG роста «Онлайн Таро» — месяцы 2–3 (серия U, web-only)

> MVP T01–T30 done (см. `docs/BACKLOG.md`). Здесь — только рост 2–3.
> Источник правды — `docs/project-book/`. Статусы: `- [ ] todo` → `🔄 doing` → `- [x] done (дата)` / `BLOCKED`.
> Правила: 1 задача = 1 ветка `feat/Uxx-...` = 1 PR по DoD (`06-rules/03-dod-frozen-scope.md`).
> Гейт мес.3: D7 ≥15%, conv free→pay ≥2%, MRR ≥30k₽, churn <15%/мес, AI-cost <10%.
> Стоп-кран рекламы: `spread_open→reading_done <60%`, `reading→paywall >30% без pay`, `paywall→pay <2%`.
> Вне скоупа (мес. 4–6): дневник, Boosty/ЮKassa, сезонные спреды сверх ротации, натив (только при 1000 платных).
> Детали закрытых — в git-истории (`BACKLOG_GROWTH.md` до чистки 2026-09-23).

## Итоги кодовой части (done 2026-09-23)
- **G0:** gateway готов, `deploy/ai-budget.sql` (ALERT >$5); живой вызов ждет ключ (U01-код).
- **G2 (U11–U14):** OG-генератор 1200×630, публичная `/share` (noindex), кнопки TG/копировать, событие `share_done`.
- **G3-код (U15–U18):** force-dynamic SSR каталога, 78 страниц `/cards/[id]`, sitemap + robots, JSON-LD.
- **G4-код (U20–U25):** стрик MSK + бейдж, web-push stdlib (VAPID/subscribe/prefs/opt-in), A/B цен, онбординг, TOP1-процесс, remind-expiring.
- **G5 (U26–U29):** single-промо при 3+ упорах, upsell-баннер, winback-флаг, рефералка v2 в профиле.

## Ждут внешнего (ключи/трафик/бот) — НЕ ТРОГАТЬ кодом
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

## Прочее BLOCKED трафиком
- [ ] U19 Дашборд SEO-доли — BLOCKED: нужен месяц живого трафика — связь: `07-roadmap/04`

## G6 — Гейт месяца 3 — BLOCKED: нужен живой трафик
- [ ] U30 Разбор D7≥15% — BLOCKED — связь: `07-roadmap/04`
- [ ] U31 Разбор денег — BLOCKED — связь: `07-roadmap/04`
- [ ] U32 Решение гейта — BLOCKED — связь: `09-adr/00`

## Статус: код готов, 13 пунктов ждут внешнего (см. `BACKLOG_OWNER.md` E01–E04).
