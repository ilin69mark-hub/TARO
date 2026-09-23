# BACKLOG владельца «Онлайн Таро» — пакет E (совместная работа, не код агента)

> Источник — строгий аудит 2026-09-23. Статусы: `- [ ] todo` → `- [x] done (дата)`.
> Правило: идем сверху вниз; каждый пункт закрываем вместе (ты делаешь внешнее — я проверяю/подключаю).
> Связи — `docs/project-book/`.

## E1 — Ключи и доступы
- [ ] E01 OpenRouter API key (+2-й для breaker) → env VPS; я проверю живой вызов U01 — связь: `04-architecture/06`
- [ ] E02 TG-бот: токен + Stars + Secret-Token → env; я проверю U02 + G1 — связь: `04-architecture/07`
- [ ] E03 PostHog-ключ → env/Next public; я проверю U03 + сверку ±5% — связь: `04-architecture/08`
- [ ] E04 Свой TG-ID в admin-whitelist; Cloudflare CDN; домен `taro.me` + диплинк — связь: `02-functional/07`
- [ ] E05 VAPID-прод ключи (сейчас dev) — связь: `U21`

## E2 — Деньги и KYC
- [ ] E06 Boosty-аккаунт + пройти модерацию эзотерики — связь: `04-architecture/07`
- [ ] E07 ИП/самозанятость + KYC для ЮKassa (вердикт №3 при >100 платных) — связь: `04-architecture/07`
- [ ] E08 Сверить курс Stars (199 Stars ≈ 299₽) в Bot API перед продом — связь: `02-functional/05`
- [ ] E09 Решение 299→349 (A/B смотрит U22) + MRR-формула подтверждена — связь: `07-roadmap/04`

## E3 — Железо и VPS
- [ ] E10 VPS: UFW/ss-check 8081, cron из deploy/README, деплой по тегу до 18:00 МСК — связь: `10-appendices/03`
- [ ] E11 FPS/LCP-замеры iPhone 12 + Moto G54 + R3F-скрин в PR — связь: `03-nonfunctional/01`
- [ ] E12 Бэкап cron + проверка восстановления — связь: `10-appendices/03`

## E4 — Контент и юрлица
- [ ] E13 22 арта вручную (батчи по 10, брак-чеклист) + домасть 78 — связь: `05-design/08`
- [ ] E14 Лицензия commercial use (генератор+тариф+дата) в `10-appendices/02-links.md` — связь: `05-design/08`
- [ ] E15 Оферта + privacy: тексты и 2 ссылки в футер до запуска 50 друзей — связь: `08-risks/02`
- [ ] E16 Crisis-номер: сверить актуальность (в Книге расхождение `8-800-7000-600` vs `8-800-...`) — связь: `08-risks/02`
- [ ] E17 Вычитка дисклеймеров 18+ везде — связь: `08-risks/02`
- [ ] E18 Сезонные окна (fullmoon/newyear) включить решением в админке — связь: `02-functional/04`

## E5 — Трафик и решения
- [ ] E19 50 друзей + 80 чтений, 0×500 на paywall — связь: `10-appendices/03`
- [ ] E20 Неделя живого трафика → baseline воронки (U04) — связь: `07-roadmap/04`
- [ ] E21 Месяц трафика → SEO-доля (U19), разбор D7/денег (U30–U31) — связь: `07-roadmap/04`
- [ ] E22 Стоп-кран рекламы: критерии + решение масштабировать/стопать — связь: `10-appendices/04`
- [ ] E23 Гейт мес.6 + ADR (натив при 1000 / углубление веба) — связь: `09-adr/00`
- [ ] E24 Разбор по понедельникам 30 мин (процесс TOP1_PROCESS.md) — связь: `07-roadmap/02`

## Статус: открыт (E01–E24 todo). Стартуем с E01, когда скажешь.

## D-пакет (код агента, 2026-09-23+)
- [x] D-тесты до 70% — done (2026-09-23): Go **70.4%** (testutil + e2e на живых PG/Redis: entitlements-порядок, readings-флоу/SSE/crisis, referral-antifraud+hook, payments webhook-duplicate/winback/refund, ratelimit 429, spreads-кэш, admin-auth/publish/audit/rotate, auth-HMAC/сессии/merge, ai mock-OpenRouter/worker, push-крипто+send, diary, me); web vitest **14/14** (device/SSE/events/paywall/streak) + `npm test` в CI; `go test` в CI с сервисами+migrate. По пути пойманы и исправлены: referral self-row КРИТБАГ (миграция 014), worker NULL-question, pgx $1-дубли. Остаток: живые ключи (E01–E03), ручные FPS/арты.
- [x] D1 admin-auth + publish/audit — done: taro_admin JWT 12ч + whitelist-промоушн + RequireAdmin на всех ручках; publish применяет diff (app_config/plans-версиями/spreads) + audit + DEL; config полный; e2e login/403/422/audit; по пути: ADMIN_TG_IDS в compose, off-by-one плейсхолдеров
- [x] D2 unit-тесты ядра + CI — done: entitlements (MSK-midnight, ISO-границы, Lua e2e), readings (draw/rate/crisis/MSK), payments (бакет/itoa), referral (формат/уникальность); CI: PG+Redis сервисы + migrate + go test; YAML valid; 9/9 пакетов зелено
- [x] D3 invoice/verify/refund — done: invoice идемпотентный (миграция 013, e2e 1 строка), verify по спеке (оба поля), refund 502 без порчи при ошибке TG + note при ручном; e2e все пути
- [x] D4 аналитика live — done: posthog-js lazy-init, visit/spread_open заведены, PII-grep в CI (clean); ключ — E03
- [x] D5 UI-мелочи + a11y — done: префиксы убраны, цена онбординга из plans, spread name, aria-labels, focus-gold, aria-live/alert, Escape в модалках; e2e имя на странице
- [x] D6 шрифты + иконки + Stars-кнопка — done: next/font Cormorant+Inter cyrillic self-host, SVG-иконки таб-бара, pay() через прокси (TG openInvoice/новая вкладка); e2e invoice-proxy 403-gate, иконки и font-display в HTML
- [x] D7 хореография — done: Fan stagger .08 из колоды, letterbox 1.2с skippable, CA-flip 300мс на картах, maath-damp камера (Arrival→Reading топ-даун), gyro-gate (fine 0.03/touch 0.01, без пермишна), лестница гарда 1→2→3 + Lite, FM-spring paywall, lenis-лендинг; e2e home 200, ca-flip в HTML
