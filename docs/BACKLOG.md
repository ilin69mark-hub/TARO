# BACKLOG «Онлайн Таро» — web-only PWA (натив запрещен до 1000 платных, ADR-05)

> Источник правды — `docs/project-book/`. Статусы: `- [ ] todo` → `🔄 doing` → `- [x] done (дата)`.
> Правила: 1 задача = 1 ветка `feat/Txx-...` = 1 PR по DoD (`06-rules/03-dod-frozen-scope.md`).
> KPI-гейт нед.7: 50 друзей, ≥80 чтений, 0×500 на paywall, LCP лендинга <2.5s + LCP 3D <4.0/<5.0s.
> Стоп-кран рекламы: `spread_open→reading_done <60%`, `reading→paywall >30% без pay`, `paywall→pay <2%` — стопаем.

## Эпик 0 — Скелет
- [x] T01 Скелет монорепо + CI — done (2026-09-23): lint clean, build clean, First Load 87.4KB <180KB; next поднят до 14.2.35 (security) — GWT: Given пустой репо, When `lint+build+bundle`, Then зелено (Next14+Go1.22, ESLint, golangci, bundle<180KB без 3D, Lighthouse warn) — связь: `06-rules/01,02`
- [x] T02 Docker Compose — done (2026-09-23): config valid, images built, healthz public+admin ok, admin 8081 NOT published ({"8081/tcp":null}), nginx→web 200, nginx→api 404 (T05 впереди); по пути починены web/public, api-public в edge-сети — GWT: Given VPS $10, When `compose up`, Then web:3000, api-public:8080, api-admin:8081 только 127.0.0.1, PG16, Redis7, nginx deny 8081, UFW — связь: `04-architecture/01`

## Неделя 1 — База
- [x] T03 Миграции БД (пакет A) — done (2026-09-23): 13 таблиц up/down без ошибок, 20 ограничений проверены; по пути пойман баг FK payments.plan_code→plans(code) (у plans композитный UNIQUE) — plan_code теперь денормализация без FK — GWT: Given `migrate up`, When проверяю схему, Then UNIQUE(user,key), single_entitlements, price_snapshot, CHECK-статусы, referrals.code UNIQUE — связь: `04-architecture/03`
- [x] T04 Seed: 78 карт + 5 spreads + 4 plans + admin — done (2026-09-23): counts 78/5/4/9/1, love НЕ premium, down откатывает сиды чисто — связь: `04-architecture/03`, `02-functional/04`
- [x] T05 `GET /v1/spreads` + Redis 5мин + Publish — done (2026-09-23): miss→5, TTL 297с, деактивация скрыта кэшем, Publish invalidated:1 → 4; Go поднят 1.22→1.25 (pgx/redis требуют) — GWT: Given выключаю spread, When Publish, Then DEL <5с, без Publish ≤5мин — связь: `04-architecture/04,05`
- [x] T06 Каркас 5 экранов кодом (без Figma, решение 2026-09-23) — done (2026-09-23): 5 роутов + таб-бар, lint/build зелено, 87.4KB — GWT: Given `npm run dev`, When открываю 5 роутов на 360px, Then структура/таб-бар/плейсхолдеры по дизайн-системе, линт+билд зеленые — связь: `05-design/01,04`
- [x] T07 Аналитика-спека — done (2026-09-23): 08-analytics-spec (7 событий, бэкап SQL, стоп-кран) + web/lib/analytics.ts (no-op без ключа), lint+tsc чисто — GWT: Given PostHog free, When событие, Then `visit/spread_open/reading_done/paywall_show/pay_success/trial_start/delete_me` + PG-бэкап — связь: `07-roadmap/02`

## Неделя 2 — Бэк
- [x] T08 Auth TG + anon — done (2026-09-23): HMAC unit-тесты ok/bad/stale, anon same-uuid→same-user, cookie HttpOnly/Secure/None 30д, forged→401; secrets проброшены в compose — связь: `02-functional/02`
- [x] T09 Merge Link + trial — done (2026-09-23): attach/trial, merge B→A с удалением дубля, trial 1 раз, идемпотентный relink, 409 на чужой tg, 401 без auth — связь: `02-functional/02,05`
- [x] T10 Refresh/logout — done (2026-09-23): sess: rotation+logout, RequireAuth проверяет sess (merge-loser инвалидируется), глобальный RequireCSRF (403 без X-CSRF) — связь: `04-architecture/04`
- [x] T11 EntitlementsService — done (2026-09-23): порядок valid→love→single→daily, Lua INCR+EXPIREAT (проверен: 1 затем −1), midnight MSK, /v1/entitlements/me (free 1/1) — связь: `02-functional/05`
- [x] T12 Readings + SSE — done (2026-09-23): draw/idempotency-до-списания/SSE/GET/история/locked/crisis-без-списания; e2e same-key→same-id, 402, filtered — связь: `02-functional/03`
- [x] T13 AI-gateway — done (2026-09-23): стрим 8с×2+fallback-модель, breaker 5/мин→5мин, ai_logs, стоп-фильтр, worker 30с, живой SSE-tee; e2e без ключа→fallback done; unit стоп-слова/хэш; остаток: живой ключ OpenRouter проверить в T30 — связь: `04-architecture/06`
- [x] T14 Love-weekly + referral — done (2026-09-23): apply→pending→hook; anon→rejected, TG→completed+бонусы обоим; по пути: миграция 005 (убрал CHECK !=), pending свой код (UNIQUE); e2e оба пути — связь: `02-functional/06`
- [x] T15 DeleteMe + 18+ — done (2026-09-23): DELETE стирает users каскадом+ai_logs анонимно+sess/keys, токен мертв; age 422/200; по пути: убран internal:true (Docker сбрасывал published-порты), хост-порты через env — связь: `08-risks/02`

## Неделя 3 — Веб Lite (без 3D)
- [x] T16 Флоу расклада Lite — done (2026-09-23): proxy /api/*, auth-bootstrap, SSE-клиент, spreads-live, spread-деталь, result с именами; e2e anon→create→result→SSE; фиксы: API_INTERNAL_URL, cp .next после rm, commit image — связь: `02-functional/03`
- [x] T17 История + поиск premium — done (2026-09-23): GET proxy, locked-blur на result, q→403 free; e2e list+q — связь: `02-functional/03`
- [x] T18 Paywall из конфига — done (2026-09-23): GET /v1/plans (3 тарифа, DISTINCT), PaywallSheet, profile (лимиты/тарифы/рефералка+apply); e2e plans/referral/profile; уроки: сиды только 1 раз (нужен migrate-трекинг в T30), Next fetch-кэш 60с дает stale — связь: `02-functional/05`
- [x] T19 PWA — done (2026-09-23): manifest+иконки(PIL эмблема)+SW(shell/spreads/cards)+install после 2-го; e2e 200 на manifest/sw/icon — связь: `03-nonfunctional/06`
- [x] T20 Юр-экраны — done (2026-09-23): AgeGate, спокойный режим, удаление в профиле, футер-дисклеймер; e2e DELETE→ok→401; урок: cp-бинарь слетает при recreate — коммитить image (сделано для api+web) — связь: `08-risks/02`

## Нед. 4–5 — Ultra
- [x] T21 R3F-база lazy — done (2026-09-23): пины three/fiber/drei/postprocessing/maath, FogQuad GLSL, dynamic ssr:false+Suspense, Lite-гейты, mounted на result; lint/tsc/build зелено — связь: `05-design/08`
- [x] T22 Foil-карты + tilt — done (2026-09-23): procedural рубашка canvas, edge-gilding fresnel, tilt desktop-only; lint/tsc/build зелено — связь: `05-design/08,09`
- [x] T23 Пост Bloom→CA→Grain→Vignette + ACES — done (2026-09-23): EffectComposer desktop-only (Bloom/Noise/Vignette), ACES 1.1, mobile прямой рендер; CA отложен на DOM-переворот T28 — связь: `05-design/08`
- [x] T24 Арт-контракт — done (2026-09-23): CardArt с fallback, image_key в API, public/cards/README (промпт-ядро+процедура+лицензия-слот); генерация 22 — ручной шаг владельца; e2e image_key в ответе — связь: `05-design/08`

## Нед. 6 — Храм Premium
- [x] T25 Сцена храма — done (2026-09-23): procedural пол/6 колонн/купол, reflector desktop + fake mobile; lint/tsc/build зелено — связь: `05-design/09`
- [x] T26 Свет static + god-rays — done (2026-09-23): key spot (тени desktop-only) + rim + candle static (flicker запрещен WCAG) + 2 god-rays planes — связь: `05-design/09`
- [x] T27 Пыль 600/150/0 + камера — done (2026-09-23): GPU-пыль с паузой, камера Arrival+clamp ≥2.2м, gyro только desktop — связь: `05-design/09`
- [x] T28 FPS-гард + a11y — done (2026-09-23): гард <28fps→лестница (пост→пыль), <22fps→Lite-событие; calm/reduced→Lite; canvas aria-hidden+DOM-дубль; CA отложен (перевороты DOM); замер на эталонах — ручной шаг владельца — связь: `03-nonfunctional/01,04`

## Нед. 7 — Запуск
- [x] T29 Stars-оплата — done (2026-09-23): invoice age-gate 403, webhook Secret-Token+идемпотентность (ok/duplicate), verify, refund с урезанием valid_until, single_99='any' (миграция 006); e2e все пути; живой TG-токен — на проде T30 — связь: `04-architecture/07`
- [x] T30 Инфра + запуск 50 — done (2026-09-23): migrate.sh с трекингом (проверен на fresh DB v6/78 карт), backup.sh ротация 30д, deploy.yml по тегам, deploy/README (UFW/SSH-туннель/откат); финальный смок: healthz×2, admin isolated, fresh→reading→402, history, result с именами, PWA 200 — связь: `10-appendices/03`

## После MVP (не трогать до гейта)
- Should мес.2–3: бот «карта дня» 21:00, OG-шеринг (AI-кэш готов с T12).
- Could мес.4–6: дневник, сезонные спреды, Boosty/ЮKassa, web-push.
- Натив — только при 1000 платных (ADR-05); любой нативный PR до этого отклоняется.

## Статус: MVP v1.0 готов (T01–T30 done). Остатки для прода:
# Остатки — только с живыми ключами/железом (не код):
# - OPENROUTER_API_KEY → живой AI-тест; TG_BOT_TOKEN + Stars → живой платеж;
# - VPS: UFW/ss-check, cron backup, замер FPS на iPhone 12 + Moto G54;
# - генерация 22 артов (промпты в public/cards/README.md) + лицензия в links.

## После MVP (не трогать до гейта)
- Should мес.2–3: бот «карта дня» 21:00, OG-шеринг, AI-кэш уже (в T12) — по метрикам.
- Could мес.4–6: дневник, сезонные спреды, Boosty/ЮKassa, web-push.
- Натив — только при 1000 платных (ADR-05); любой нативный PR до этого отклоняется.
