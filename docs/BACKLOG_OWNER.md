# BACKLOG владельца «Онлайн Таро» — пакет E (совместная работа, не код агента)

> Источник — строгий аудит 2026-09-23. Статусы: `- [ ] todo` → `- [x] done (дата)`.
> Правило: идем сверху вниз; каждый пункт закрываем вместе (ты делаешь внешнее — я проверяю/подключаю).
> Связи — `docs/project-book/`.

## E1 — Ключи и доступы
- [ ] E01 OpenRouter API key (+2-й для breaker) → env VPS; я проверю живой вызов U01 — связь: `04-architecture/06`
- [ ] E02 TG-бот: токен + Stars + Secret-Token → env; я проверю U02 + G1 — связь: `04-architecture/07`
- [ ] E03 PostHog-ключ → env/Next public; я проверю U03 + сверку ±5% — связь: `04-architecture/08`
- [ ] E04 Provisioning пароля администратора; Cloudflare CDN; домен `taro.me` + диплинк — связь: `02-functional/07`
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
- [x] E14 Лицензия commercial use — ✅ 2026-09-28: ChatGPT (OpenAI), тариф ChatGPT Go, commercial use по Terms of Use effective 2026-01-01 («you own the Output»), 2 маркера `/// ⛔` сняты. Открыто: маркировка ИИ по 420-ФЗ ст. 13 — связь: `05-design/08`
- [ ] E15 Оферта + privacy: тексты и 2 ссылки в футер до запуска 50 друзей — связь: `08-risks/02`
- [ ] E16 Crisis-номер УБРАН из кода — владелец вписывает сам: см. `docs/OWNER_TODO.md` (3 маркера `/// ⛔`) — связь: `08-risks/02`
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
- [x] D1 admin-auth + publish/audit — done: taro_admin JWT 12ч + password login/bcrypt + RequireAdmin на всех ручках; publish применяет diff (app_config/plans-версиями/spreads) + audit + DEL; config полный; e2e login/403/422/audit; off-by-one плейсхолдеров закрыт
- [x] D2 unit-тесты ядра + CI — done: entitlements (MSK-midnight, ISO-границы, Lua e2e), readings (draw/rate/crisis/MSK), payments (бакет/itoa), referral (формат/уникальность); CI: PG+Redis сервисы + migrate + go test; YAML valid; 9/9 пакетов зелено
- [x] D3 invoice/verify/refund — done: invoice идемпотентный (миграция 013, e2e 1 строка), verify по спеке (оба поля), refund 502 без порчи при ошибке TG + note при ручном; e2e все пути
- [x] D4 аналитика live — done: posthog-js lazy-init, visit/spread_open заведены, PII-grep в CI (clean); ключ — E03
- [x] D5 UI-мелочи + a11y — done: префиксы убраны, цена онбординга из plans, spread name, aria-labels, focus-gold, aria-live/alert, Escape в модалках; e2e имя на странице
- [x] D6 шрифты + иконки + Stars-кнопка — done: next/font Cormorant+Inter cyrillic self-host, SVG-иконки таб-бара, pay() через прокси (TG openInvoice/новая вкладка); e2e invoice-proxy 403-gate, иконки и font-display в HTML
- [x] D7 хореография — done: Fan stagger .08 из колоды, letterbox 1.2с skippable, CA-flip 300мс на картах, maath-damp камера (Arrival→Reading топ-даун), gyro-gate (fine 0.03/touch 0.01, без пермишна), лестница гарда 1→2→3 + Lite, FM-spring paywall, lenis-лендинг; e2e home 200, ca-flip в HTML
- [ ] D8 `ip_cluster_limit` рефералки — **решить, включать ли (пока 0 = выкл)**. Правки антифермы закрыты (миграции 037/038/039, `docs/project-book/02-functional/06-referral.md`): парные проверки fingerprint/IP + кластер fingerprint (`fingerprint_cluster_limit=3`, дефолт выбран мной — тоже стоит подтвердить). Остался один осознанно нерешённый размен:
  - **За включение:** ловит ферму «чужой код + 10 tg-аккаунтов с одного IP с чистыми fingerprint'ами». Red-team: без порога 10/10 completions, с `ip_cluster_limit=4` → 3/10.
  - **Против:** CGNAT мобильных операторов сажает десятки честных пользователей за один адрес.
  - ⛔ **Блокер, найденный при рестарте:** `antifarm_ip` и `ip_cluster_limit` нельзя включать **до настройки real_ip в nginx**. В `deploy/nginx.conf` нет `set_real_ip_from`, IP берётся из `$remote_addr`. Стоит поставить Cloudflare (E04) — `$remote_addr` станет edge-IP Cloudflare, одинаковым для всех, и проверка по IP начнёт отклонять почти все легитимные рефералки. Поэтому миграция 039 выключает `antifarm_ip` по умолчанию. **Шаги:** (1) `set_real_ip_from` со списком диапазонов Cloudflare + `real_ip_header CF-Connecting-IP` в `deploy/nginx.conf` и `nginx-tls.conf`; (2) проверить, что `X-Real-IP` = реальный адрес клиента; (3) тогда включать `antifarm_ip=true` и подбирать `ip_cluster_limit`.
  - **Метрика для решения:** `SELECT count(*), count(DISTINCT referee_ip) FROM referrals WHERE status='completed' AND created_at > now() - interval '7 days'` — если max по нормальным дням ≤3, порог 5 безопасен. Связь: `02-functional/06`
- [ ] D9 **real_ip в nginx (блокер для IP-лимитов и антифермы)** — тот же корень, что D8, но шире и независим от рефералки. Тот же `$remote_addr` попадает в ключ `ratelimit` для `byUser=false` (`/v1/auth/`, `/v1/spreads`, `/v1/share`): без Cloudflare все анонимные посетители делят **один** бакет (60/мин на всех), с Cloudflare — IP станет edge-адресом. Нужно: `set_real_ip_from` (Cloudflare + свои прокси, если есть) + `real_ip_header` в обоих конфигах. Проверка после — зайти с разных адресов и убедиться, что `/v1/spreads` не отдаёт 429 общим бакетом. Связь: `04-architecture/06`
- [ ] D10 Подтвердить дефолты `fingerprint_cluster_limit=3` и `lifetime_cap_days=300` — выбраны мной как продуктовый размен, не по требованиям. `fingerprint_cluster_limit` — НЕ граница безопасности (сервер одинаково видит семью за ноутбуком и ферму из одного браузера); настоящие границы ущерба — месячный кэп 30 дней + цена tg-аккаунта. `lifetime_cap_days=300` = 100 рефералов за жизнь. Связь: `02-functional/06`

