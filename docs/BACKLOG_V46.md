# BACKLOG «Онлайн Таро» — месяцы 4–6 (серия V, web-only до 1000 платных)

> T01–T30 (MVP) done — см. `docs/BACKLOG.md`. U-серия (рост 2–3) — см. `docs/BACKLOG_GROWTH.md`.
> Источник правды — `docs/project-book/`. Статусы: `- [ ] todo` → `🔄 doing` → `- [x] done (дата)` / `BLOCKED`.
> Правила: 1 задача = 1 ветка `feat/Vxx-...` = 1 PR по DoD (`06-rules/03-dod-frozen-scope.md`).
> Гейт мес.6: 1000 платных → натив, иначе углубление веба (см. `07-roadmap/03`).
> Натив запрещен кодом до гейта (DoD web-only); V28 — только исследование + ADR-черновик.

## W0 — Прод-хвосты (внешние)
- [ ] V01 Живой AI e2e — BLOCKED: нужен OPENROUTER_API_KEY — связь: `04-architecture/06`
- [ ] V02 Живой Stars e2e — BLOCKED: нужен TG-бот с Stars — связь: `04-architecture/07`
- [ ] V03 FPS-замеры на эталонах — BLOCKED: нужно железо iPhone 12 + Moto G54 — связь: `03-nonfunctional/01`
- [ ] V04 Генерация 22 артов — BLOCKED: ручной шаг владельца (промпты в `public/cards/README.md`) — связь: `05-design/08`

## W1 — Дневник
- [x] V05 Миграция diary_entries — done (2026-09-23): user CASCADE, reading SET NULL, mood CHECK, индексы; e2e up — связь: `04-architecture/03`
- [x] V06 CRUD API дневника — done (2026-09-23): create/list/get/put/delete, чужие→404, mood/body валидация; e2e curl + Go-тест PUT/DELETE — связь: `02-functional/01`
- [x] V07 UI-редактор — done (2026-09-23): черновик-автосейв, настроения, proxy /api/diary*; lint/tsc/build зелено — связь: `05-design/04`
- [x] V08 Связка чтение↔запись — done (2026-09-23): кнопка из result + ?reading= в редакторе — связь: `02-functional/03`
- [x] V09 Настроение-теги — done (2026-09-23): фильтр по mood в UI + API; e2e фильтр — связь: `02-functional/01`
- [x] V10 Каскад DeleteMe — done (2026-09-23): e2e DELETE users → 0 записей (ON DELETE CASCADE) — связь: `08-risks/02`
- [x] V11 Стрик учитывает записи — done (2026-09-23): UNION readings+diary в HandleStreak; e2e diary-only день → 1 — связь: `04-architecture/08`
- [x] V12 Экспорт записей — done (2026-09-23): GET /v1/diary/export + proxy + кнопка; e2e JSON — связь: `08-risks/02`

## W2 — Платежи
- [x] V13 Абстракция провайдеров — done (2026-09-23): интерфейс Provider, Stars через него без смены поведения — связь: `04-architecture/07`
- [ ] V14 Boosty — BLOCKED: нужен Boosty-аккаунт — связь: `04-architecture/07`
- [x] V15 ЮKassa-скелет — done (2026-09-23): 501 без KYC за флагом, 422 неизвестный; e2e — связь: `04-architecture/07`
- [x] V16 Механика winback-скидки — done (2026-09-23): offers.winback pct + eligibility, цена в invoice; e2e 239 + winback-20; по пути пойман pgx-баг (повтор $1 + 2 args = ошибка, молча съедена) — чинены оба места — связь: `02-functional/07`
- [x] V17 Invoice со скидкой + аудит — done (2026-09-23): payments.note (миграция 009), снапшот 239 виден — связь: `04-architecture/03`
- [ ] V18 Возврат Boosty — BLOCKED: зависит от V14 — связь: `04-architecture/07`
- [x] V19 Админ-список платежей — done (2026-09-23): GET /v1/admin/payments (статусы/суммы/note); e2e — связь: `02-functional/07`
- [x] V20 Отчет amount_rub — done (2026-09-23): deploy/revenue.sql по провайдерам; e2e 0 строк (dev) — связь: `07-roadmap/04`

## W3 — Сезоны и пуши
- [x] V21 Пак сезонных спредов — done (2026-09-23): миграции 010 (CHECK) + 011 (fullmoon/newyear inactive); e2e 7 раскладов — связь: `02-functional/04`
- [x] V22 Авторотация по датам — done (2026-09-23): POST /v1/admin/rotate-seasonal по spreads.seasonal-окнам + инвалидация; e2e changed:1; cron — в deploy-доку — связь: `02-functional/07`
- [x] V23 Вечерний пуш-контент — done (2026-09-23): 7 шаблонов ротация по дню недели, /v1/admin/push-evening; e2e — связь: `05-design/07`
- [x] V24 Настройки пушей — done (2026-09-23): миграция 012 (prefs+logs), GET/POST /v1/push/prefs, UI в профиле, фильтр hour/quiet в рассылке; e2e — связь: `03-nonfunctional/06`
- [x] U25-наследник: стрик+пуш — done (2026-09-23): /v1/admin/push-streak-risk (вчера+дни≥1); e2e — связь: `04-architecture/08`
- [x] V26 Аналитика пушей — done (2026-09-23): push_logs + /v1/admin/push-stats 7д (без пикселя); e2e — связь: `04-architecture/08`

## W4 — Натив-гейт и закалка
- [x] V27 Дашборд платных — done (2026-09-23): deploy/paid.sql (active vs 1000, gate_pct); e2e 0/1000; cron-дока (backup/rotate/evening/streak/remind) в deploy/README — связь: `07-roadmap/04`
- [x] V28 Expo-spike — done (2026-09-23): docs/EXPO_SPIKE.md без кода (переиспользование 40%, цена 3–4 нед, триггер gate_pct≥100 ×2 мес) — связь: `09-adr/05`
- [x] V29 Gap-лист PWA vs native — done (2026-09-23): таблица в EXPO_SPIKE (пуши iOS, стор, виджеты, Face ID) — связь: `09-adr/05`
- [x] V30 Ревью перф-бюджетов — done (2026-09-23): First Load 87.5KB<180, spreads Redis-hit ~30–50мс<150, DB 9MB; cold 0.8с — норма — связь: `03-nonfunctional/01`
- [x] V31 Security-реаудит — done (2026-09-23): Go rate-middleware (e2e 429), секретов нет, CSRF/webhook/admin зеленые; next GHSA задокументирован с митигациями — связь: `03-nonfunctional/02`
- [ ] V32 Гейт-решение + ADR — BLOCKED: нужны цифры мес.6 — связь: `09-adr/00`

## Статус: кодовая часть готова (W1, V13, V15–V17, V19–V31 кроме блокеров).
# BLOCKED внешним (не код) — для владельца:
# - V01–V04 (ключи/железо/арты), V14/V18 (Boosty-аккаунт), V32 (цифры мес.6).
# BLOCKED внешним: V01–V04 (ключи/железо/арты), V14/V18 (Boosty), V32 (цифры).
