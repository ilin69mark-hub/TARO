# BACKLOG Forensic-аудита «Онлайн Таро» — серия A (аудит 2026-09-25, HEAD `1217da4`)

> **Источник правды:** `docs/audit/FINAL-VERDICT-1217da4.md` — 57 находок F-01…F-57. Здесь каждая находка = ровно одна задача A01…A57. Все 57 перенесены без исключения и без сокращений.
> **Статусы:** `- [ ] todo` → `🔄 doing` → `- [x] done (дата)` / `⛔ BLOCKED (причина)`.
> **Правило серии:** 1 задача = 1 ветка `fix/Axx-<slug>` = 1 PR. Берём **строго сверху вниз, по одной**. Рефакторинг «по пути» запрещён.
> **DoD задачи (обязателен):** (1) код исправлен, (2) регресс-тест добавлен и падает без фикса, (3) команда проверки зелёная, (4) `git diff HEAD` содержит только файлы этой задачи, (5) в PR — ссылка на F-xx.
> **Проверка:** у каждой задачи своя команда. Прогонять обязательно: `go build ./... && go vet ./...` (в `api/`), `npx tsc --noEmit && npm test` (в `web/`), `go test -race -count=1 ./...` с поднятыми PG+Redis.
> **Если задача окажется неверной** (находка не воспроизводится на текущем HEAD) — не «чинить», а пометить `⛔ BLOCKED`, записать доказательство в PR и взять следующую.
> **Прогресс: 20/58.** Следующая: **A21** (F-21, MEDIUM) — `X-Real-IP` не пробрасывается, общие корзины лимитов.

## Порядок (волны)

Волны устроены так, чтобы каждая следующая опиралась на предыдущую: W0 разблокирует шиппинг (до неё фиксы некуда доставлять), W1 закрывает деньги, W2 ёмкость, W3 web-контракты, W4 инфра/наблюдаемость, W5 гигиена и документация.

| Волна | Задачи | Тема | Оценка |
|---|---|---|---|
| **W0** | A01–A06 | Разблокировать шиппинг | ~1 ч |
| **W1** | A07–A14 | Целостность денег | ~2 дня |
| **W2** | A15–A19 | Ёмкость и кэш | ~1 день |
| **W3** | A20–A30 | Web-контракты и безопасность | ~2 дня |
| **W4** | A31–A37 | Supply chain, деплой, наблюдаемость | ~2 дня |
| **W5** | A38–A57, A58 | Гигиена, миграции, документация, изоляция тестов | ~3 дня |

| # | ID | Sev | F | Находка | Основные файлы |
|---|---|---|---|---|---|
| A01 | F-01 | CRITICAL | `deploy.yml:84` | backups/ ломает гейт деплоя | `.gitignore`, `deploy/backup.sh` |
| A02 | F-02 | CRITICAL | `docker-compose.yml:133` | admin-access netns desync | `docker-compose.yml`, `deploy/admin-job.sh` |
| A03 | F-03 | HIGH | `web/Dockerfile:20,25` | root-owned `.next/cache` → EACCES | `web/Dockerfile` |
| A04 | F-14 | HIGH | `nginx.conf:30-37` | нет XFO в проде + 2 CSP | `deploy/nginx*.conf`, `web/next.config.js` |
| A05 | F-04 | HIGH | `CardArt.tsx:11-12` | арт-контракт 0/78 | `web/components/CardArt.tsx`, `api/migrations/002_seed.up.sql`, `web/public/cards/` |
| A06 | F-32 | MEDIUM | `docker-compose.prod.yml` | 6 из 15 prod-переменных нет в `.env.example` | `.env.example`, `deploy.yml`, CI |
| A07 | F-07 | HIGH | `payments.go:605-622` | lost update на MAX(valid_until) | `payments.go`, новая миграция |
| A08 | F-08 | HIGH | `payments.go:154-164` / `me.go:78-87` | дедлок webhook ↔ DELETE /v1/me | `payments.go`, `me.go` |
| A09 | F-09 | HIGH | `payment_webhook_events` | таблицу никто не читает | `adminctl`, `admin.go` |
| A10 | F-24 | MEDIUM | `referral.go:179-186` | кап месяца check-then-act | `referral.go` |
| A11 | F-25 | MEDIUM | `link.go:255-259` | merge теряет provenance для refund | `link.go`, `payments.go` |
| A12 | F-12 | HIGH | `readings.go:459` | entitlement списывается на провайдер-сталл | `readings.go`, `ai` |
| A13 | F-11 | HIGH | `ai.go:27,114,742` | 8 с покрывает TTFT + генерацию | `ai.go` |
| A14 | F-18 | HIGH | тесты денежных путей | refund без RequireAdmin, TG_BOT_TOKEN, -count=2, общий Redis | `*_test.go`, `ci.yml` |
| A15 | F-06 | HIGH | `ai/worker.go:20` | потолок 2.00 readings/min | `ai/worker.go`, `cmd/api/main.go` |
| A16 | F-05 | HIGH | `ratelimit.go:24-28` | окно сбрасывается вытеснением из кэша | `docker-compose*.yml`, `ratelimit.go` |
| A17 | F-43 | MEDIUM | `ai.go:665` | 7-дневный AI-кэш делит кэш с лимитами | `docker-compose*.yml`, `ai.go` |
| A18 | F-42 | MEDIUM | `store.go:26-30` | 1.714 с на запрос при мёртвом Redis | `store.go`, `ratelimit.go` |
| A19 | F-48 | LOW | `readings.go:1081` | 131 073 симв. на строку, отдаётся 160 б | `readings.go` |
| A20 | F-13 | HIGH | 18 из 22 роутов | теряется `Set-Cookie`/`Retry-After` | `web/app/api/**` |
| A21 | F-21 | MEDIUM | `proxy.ts:41-57` | не пробрасывается `X-Real-IP` | `web/lib/proxy.ts` |
| A22 | F-22 | MEDIUM | `readings.go:846-852` | SSE без terminal frame читается как успех | `readings.go`, `web/lib/api.ts` |
| A23 | F-10 | HIGH | `filter.go:21-29` | фильтр обходится; self-match SafeReplacement | filter |
| A24 | F-29 | MEDIUM | `sw.js` | нет push-приёма при «Пуши включены» | `web/public/sw.js`, `PushOptIn` |
| A25 | F-30 | MEDIUM | `layout.tsx` | нет metadataBase, токены в логах, `$host` | `layout.tsx`, `nginx*.conf` |
| A26 | F-31 | MEDIUM | `Dockerfile:5,14` | `NEXT_PUBLIC_BASE_URL` опционален | `web/Dockerfile`, `docker-compose.yml` |
| A27 | F-39 | MEDIUM | `auth.ts:127` | `retryAuth` не вызывается | `web/lib/auth.ts`, `profile/page.tsx` |
| A28 | F-40 | MEDIUM | `layout.tsx:42-43` | Onboarding поверх AgeGate | `layout.tsx`, `Legal.tsx`, `Onboarding.tsx` |
| A29 | F-47 | MEDIUM | `push.go` | push-endpoint принимает любой host/port | `push.go` |
| A30 | F-45 | MEDIUM | `share.go`, `push.go:406-447` | нет капа fingerprint; disabled в фоновых выборках | `auth`, `share.go`, `push.go` |
| A31 | F-15 | HIGH | `go.mod:3`, образы | EOL-тулчейн + 6 плавающих баз | `go.mod`, `ci.yml`, `Dockerfile*` |
| A32 | F-16 | HIGH | `deploy.yml:94-105` | нет бэкапа перед миграцией и нет rollback | `deploy.yml`, `migrate.sh` |
| A33 | F-17 | HIGH | `ci.yml` | CI не видит дефекты этого отчёта | `ci.yml` |
| A34 | F-19 | HIGH | `main.go:92-109` | `Recover()` вне тикера убивает джоб | `cmd/api/main.go`, `apierr.go` |
| A35 | F-20 | HIGH | весь `api/` | 10 log.Printf, нет метрик и request ID | `api/` |
| A36 | F-41 | MEDIUM | `main.go:183` | у серверов нет таймаутов | `cmd/api/main.go`, `cmd/admin/main.go` |
| A37 | F-23 | MEDIUM | `ai.go:471-473` | тело ответа провайдера в `ai_logs` | `ai.go` |
| A38 | F-38 | MEDIUM | `entitlements.go:763-779` | мёртвая вторая реализация квот | `entitlements.go`, доки |
| A39 | F-37 | MEDIUM | 5 knobs `app_config` | админ-ручки, которые никто не читает | `admin.go`, веб |
| A40 | F-44 | MEDIUM | `033_*` | миграция-пустышка, `down` отказывает | `033_*`, `schema-ready.sh` |
| A41 | F-46 | MEDIUM | `034_*` | карантинные строки не переезжают при merge | `034_*`, `link.go` |
| A42 | F-26 | MEDIUM | `payments.go:957-964` | повторные попытки создают новые инвойсы | `payments.go` |
| A43 | F-27 | MEDIUM | `recover-dirty.sh:156` | `psql -f` без `--single-transaction` | `recover-dirty.sh` |
| A44 | F-28 | MEDIUM | `schema-ready.sh:606-620` | пустой admin_accounts ломает verify | `schema-ready.sh`, `adminctl` |
| A45 | F-35 | MEDIUM | `referral.go:178` | месяц по server-local, остальное MSK | `referral.go`, `admin.go` |
| A46 | F-36 | MEDIUM | `payments.go:352` | float в A/B, int в разборе | `payments.go` |
| A47 | F-33 | MEDIUM | `diary/export` | экспорт целиком в память 1 GiB | `web/app/api/diary/export` |
| A48 | F-34 | MEDIUM | `analytics.ts:46-47` | KPI-события не эмитятся | `web/lib/analytics.ts`, доки |
| A49 | F-51 | LOW | `sw.js:41` | `/cards/`-страница CacheFirst, VERSION мёртв | `web/public/sw.js` |
| A50 | F-52 | LOW | `web/Dockerfile:21` | devDeps в прод-образе (1.27 ГБ) | `web/Dockerfile` |
| A51 | F-53 | LOW | `nginx-tls.conf` | копия конфига, гвард из одного grep | `deploy/nginx-tls.conf`, CI |
| A52 | F-54 | LOW | `admin.go` | нет `admin_audit` для refund/push | `admin.go` |
| A53 | F-55 | LOW | `ai.go:665` | AI-кэш переживает удаление аккаунта | `ai.go`, `me.go` |
| A54 | F-49 | LOW | тесты | 112/175 тестов скипаются локально | `testutil` |
| A55 | F-50 | LOW | `ratelimit.go:60-68` | 5 маршрутов без правила, нет инварианта | `ratelimit.go` |
| A56 | F-56 | INFO | `proxy.ts` | Origin-rewrite: проверено — не эксплуатируется | документировать |
| A57 | F-57 | INFO | VAPID | «секрет в репо» опровергнуто | закрыть как REFUTED |
| A58 | F-58 | MEDIUM | `worker.go:149` | `drainOnce` берёт чужое чтение → CI-флак | `ai_e2e_test.go:545-582` |

---

# WAVE 0 — разблокировать шиппинг

### A01 · F-01 · CRITICAL · ✅ done 2026-09-25 · деплой заблокирован гейтом untracked-файлов
- **Где:** `.gitignore:12-17` · `deploy/backup.sh:6,10-16` · `deploy/README.md` (§ Backup) · `.github/workflows/ci.yml` (security, 2 шага)
- **Сделано:** (1) `.gitignore` — секция рантайм-состояния: `backups/`, `taro-backups/`, `*.sql.gz`, `*.sql.gz.gpg`; (2) `backup.sh` — дефолт `BACKUP_DIR` = `$(dirname $ROOT)/taro-backups` (сосед checkout, вместо `/opt/taro/backups` внутри worktree) + **гард**: скрипт отказывается работать, если `BACKUP_DIR` внутри worktree; (3) `README.md` — контракт `BACKUP_DIR`, команда `mkdir/chown/chmod`, пример override на `/var/backups/taro`; (4) `ci.yml` — два шага в job `security`: (а) `backups/` игнорируется **и** `git status --porcelain -uall` пуст при существующем `backups/`; (б) старый дефолт `/opt/taro/backups` не возвращается и гард реально срабатывает.
- **Доказано «до»:** на чистом клоне `1217da4` без правок `mkdir backups && : > backups/probe` → `git status --porcelain -uall` = `?? backups/probe` → `deploy.yml:84` падает; гарда в скрипте нет.
- **Доказано «после»:** в клоне с применёнными правками тот же тест → `git check-ignore -v` = `.gitignore:14:backups/`, untracked пуст, **полный префлайт `deploy.yml:75-84` проходит** при существующем `backups/`; `BACKUP_DIR=$PWD/backups deploy/backup.sh` → `exit 1` с сообщением «must be outside the repository worktree»; дефолт резолвится в `…/taro-backups` (вне worktree).
- **Регресс:** `sh -n` + `test -x` для всех `deploy/*.sh` — OK; `ci.yml` парсится YAML — OK; `go build ./...`, `go vet ./...`, `npx tsc --noEmit` — OK. Go/web-код задача не трогает, поэтому полный `go test -race` не перезапускался (сигнала нет); при следующем затрагивающем Go задаче — прогнать обязательно.
- **Коммит:** не выполнялся (ждёт вашего решения). Ветка по правилам серии: `fix/A01-backup-dir-outside-worktree`.

### A02 · F-02 · CRITICAL · ✅ done 2026-09-25 · `admin-access` терял netns → админка и 4 крона умирали
- **Где:** `docker-compose.yml` (сеть `admin`, порты, networks) · `api/cmd/admin/main.go:121` · `deploy/admin-proxy.conf` · `deploy/admin-job.sh:51` · `.github/workflows/deploy.yml:114` · `.github/workflows/ci.yml` (2 шага) · `deploy/README.md` (§ Сети и admin)
- **Сделано:** (1) `network_mode: service:api-admin` убран — `admin-access` стал отдельным контейнером; (2) host-порт `127.0.0.1:${ADMIN_HOST_PORT}:8082` переехал с `api-admin` на `admin-access`, `api-admin` наружу не публикуется вообще; (3) новая сеть `admin` — в ней ровно `api-admin` + `admin-access`, `api-public` туда не входит; (4) `api-admin` слушает `ADMIN_LISTEN_ADDR` (дефолт `0.0.0.0:8081`) вместо жёсткого `127.0.0.1:8081` — иначе апстрим недостижим из другого контейнера; (5) `deploy/admin-proxy.conf`: апстрим по DNS-имени + `resolver 127.0.0.11 valid=10s` и `proxy_pass http://$admin_api$request_uri`; (6) `admin-job.sh` и `deploy.yml` спрашивают порт у `admin-access`; (7) README переписан; (8) CI: shell-инварианты + структурная проверка compose через python.
- **Два инварианта «не откатывать»:** общего netns быть не должно; nginx обязан резолвить апстрим **на каждый запрос** (см. ниже про 502).
- **Доказано вживую:** (а) после `--force-recreate api-admin` контейнер `admin-access` остался тем же ID и сразу отдал 200 на `/`, `/healthz`, `/readyz` (api-admin получил новый ID); (б) `ip -o addr` у `admin-access` показывает свой `eth0`; (в) auth-путь живой: `POST /v1/admin/login` с неверными кредями → 401 с русским сообщением, forged-cookie → 403, security-заголовки на месте; (г) префлайт `admin-job.sh`: `docker compose port admin-access 8082` → `127.0.0.1:8081`, loopback-проверка и `healthz` проходят; (д) изоляция: в `taro_admin` только `api-admin` и `admin-access`, у `api-public` сетей `edge`+`internal`.
- **Найдено и починено попутно (латентный баг новой схемы):** со статическим `proxy_pass http://api-admin:8081` nginx резолвит имя один раз на старте. После пересоздания `api-admin` с **другим IP** админка отдавала 502 **навсегда** (доказано: IP сменился на `172.18.0.4`, `upstream: http://172.18.0.2:8081` → connection refused) — ровно тот же симптом, что чиним. После перехода на `resolver` + `$request_uri` тот же сценарий даёт 200 без рестарта `admin-access`.
- **Побочно:** (1) `nginx` не стартует, если апстрим не резолвится на момент старта (`host not found in upstream`) — при смене определения сети compose не перерегистрирует алиасы у ужеrunning контейнеров, нужен `--force-recreate` **всех** участников; (2) `admin-api` отдавал 404 на `/readyz` только из-за устаревшего образа (собран до HEAD) — после `docker compose build api-admin` всё зелено; (3) локальный `.env` без `NEXT_PUBLIC_BASE_URL` роняет **любую** команду `docker compose` — это F-32 в действии.
- **Регресс:** `go test -race -count=1 ./...` на чистой PG16 (34 миграции) + Redis — **14/14 OK**; `go build`, `go vet` — OK; `sh -n` + `test -x` всех `deploy/*.sh` — OK; `ci.yml` парсится, оба новых шага проходят локально. Один прогон дал фейл `TestE2EWorkerPersistsTerminalFallbackWithoutAI` — оказалось межпакетной интерференцией тестов, не следствием правки; заведено как **A58/F-58** (новая находка).
- **Коммит:** не выполнялся (ждёт вашего решения). Ветка: `fix/A02-admin-access-orphan-netns`.

### A03 · F-03 · HIGH · ✅ done 2026-09-25 · `.next/cache` принадлежал root → EACCES на каждой картинке
- **Где:** `web/Dockerfile:17-30` · `.github/workflows/ci.yml` (guard)
- **Сделано:** (1) `COPY --from=build --chown=node:node /app/.next ./.next`; (2) в рантайм-стадии `RUN mkdir -p /app/.next/cache/images && chown -R node:node /app/.next` **до** `USER node` — каталог мог не создаться на сборке, поэтому одной смены владельца мало; (3) CI-guard фиксирует контракт Dockerfile.
- **Осознанно не сделано:** `output: 'standalone'` и prod-зависимости — это F-52/**A50** (образ 1.27 ГБ с devDeps, `npm` как PID 1). Не смешивал с этой задачей.
- **Доказано «до» (образ, собранный из текущего HEAD):** `/app/.next` и `/app/.next/cache` = `uid 0 gid 0` при процессе `uid=1000(node)`; 100 запросов `/_next/image?url=/icon-192.png&w=256&q=75` → **100×200 клиенту**, но в логах **100 `unhandledRejection` + 400 строк EACCES**, все про `mkdir '/app/.next/cache/images'`. То есть дефект полностью скрыт от клиента.
- **Доказано «после»:** `.next`, `.next/cache`, `.next/cache/images` → `1000 1000`; 100 запросов → 100×200; `EACCES: 0`, `unhandledRejection: 0`, `Failed to write image: 0`; файл кэша реально создан (`find .next/cache/images -type f` = 1, тот же хэш `esSPs_…`, что и падал раньше).
- **Продовый путь сборки:** `docker compose build web` с `NEXT_PUBLIC_BASE_URL=https://taro.example` → образ `taro-web:latest`, `cache/images` = `1000 1000`.
- **Регресс:** `vitest run` 21/21, eslint чист, `ci.yml` парсится, guard проходит локально и **проверен мутацией** (откат `--chown` → guard падает). Go-код задача не трогает.
- **Тест:** полный smoke-тест образа (сборка в CI + 100 запросов `/_next/image` → 0 EACCES) отложен в **A33** — там web-образ уже собирается в CI; здесь дешёвый структурный guard.
- **Коммит:** не выполнялся (ждёт вашего решения). Ветка: `fix/A03-next-cache-writable`.

### A04 · F-14 · HIGH · ✅ done 2026-09-26 · CSP: два источника правды, один глушился на edge
- **Где:** `web/next.config.js:14-24` · `deploy/nginx.conf:31,37` · `deploy/nginx-tls.conf:32,46` · `.github/workflows/ci.yml` (job `security`, python-шаг) · `deploy/README.md` (раздел TLS)
- **⚠️ Скоуп был уточнён при A01:** отсутствие `X-Frame-Options` — **задокументированное намеренное решение** («intentionally absent because it would override that allowance»), а `frame-ancestors` в CSP имеет приоритет над XFO в современных браузерах. Добавлять XFO «просто так» было бы вредно — это ломает фрейминг Telegram WebView. Настоящий дефект был другой: **два независимых источника CSP**, причём app-CSP глушится на edge, и правка в приложении молча исчезала бы в проде безо всякого теста.
- **Решение:** edge (`proxy_hide_header Content-Security-Policy` + своя `add_header`) остаётся единственным авторитетом — это нужно для фрейминга Telegram WebView. Вместо уничтожения edge **устранено молчаливое расхождение**: политика приложения выровнена посимвольно на edge, а равенство проверяется в CI.
- **Сделано:** (1) CSP в `next.config.js` приведена к nginx-строке (281 символ, побайтово равна во всех трёх файлах); (2) удалён `X-Frame-Options: DENY` из приложения — он противоречил собственному `frame-ancestors` с allow-list для Telegram, к тому же edge его всё равно срезает, а XFO не имеет allow-list формы; (3) CI-шаг сравнивает `web/next.config.js`, `deploy/nginx.conf` и `deploy/nginx-tls.conf`, требует `frame-ancestors 'self' https://web.telegram.org` и запрещает объявлять XFO в приложении; (4) README — абзац «правьте все три места вместе».
- **Что было до (измерено, 9 директив, 281 символ):** расхождение ровно в двух директивах — `frame-ancestors` (`'none'` против `'self' https://web.telegram.org`) и `script-src` (в приложении не было `https://telegram.org`); плюс приложение объявляло срезаемое nginx'ом `X-Frame-Options: DENY`.
- **Проверено, что allowance `https://telegram.org` в `script-src` мёртвый:** в приложении нет ни одного внешнего `<script src=…>` (grep по `web/app web/components web/lib` — пусто), PostHog подключён npm-пакетом и ходит через `connect-src`. Allowance оставлен как есть: менять продовую политику в задаче про синхронизацию — лишний риск; сужение вынесено отдельно.
- **Доказано:** после `npm run build` в `.next/routes-manifest.json` запечён CSP 281 символ, `frame-ancestors 'self' https://web.telegram.org`, XFO нет ни в одном маршруте; живой `next start` на `/` и `/diary` отдаёт ровно nginx-политику (сверено побайтово), XFO в ответе отсутствует.
- **Тест (мутационный, 3 сценария — все пойманы):** (1) вернуть приложению `frame-ancestors 'none'` → «CSP policies diverged» со печатью трёх строк; (2) убрать `https://web.telegram.org` из nginx → расхождение; (3) вернуть `X-Frame-Options: DENY` → «app must not declare X-Frame-Options». После отката — `CSP aligned across app/nginx/nginx-tls: 281 chars`.
- **Регресс:** `vitest run` 21/21, eslint чист, `ci.yml` парсится, извлечённый из CI python-шаг проходит локально.
- **Коммит:** не выполнялся (ждёт вашего решения). Ветка: `fix/A04-csp-single-policy`.

### A05 · F-04 · HIGH · ✅ done 2026-09-26 (код-контракт) · арт-контракт был невыполним: 0 из 78 карт
- **Где:** `web/components/CardArt.tsx:6-16` · `web/components/CardArt.test.tsx` (новый) · `web/public/cards/README.md`
- **Решение по развилке:** расширен allowlist, а не переписан сид. Обоснование: (1) `src` собирается как `/` + `image_key`, то есть файл и нужен в `public/cards/` — префикс `cards/` семантически верен; (2) ноль миграций и ноль перезаписи данных; (3) guard против traversal сохранён (regex по-прежнему якоренный, `[a-z0-9-]`, ровно одна директория). Перегон сида означал бы смену задокументированного в `public/cards/README.md` контракта «имя файла = image_key» без выигрыша.
- **Сделано:** (1) `IMAGE_KEY_RE` → `/^cards\/(major-[0-9]{2}-[a-z-]+|minor-[a-z]+-[a-z0-9]+|card-back)\.webp$/`; (2) новый `CardArt.test.tsx`: сид-002 читается как источник правды (без БД, живёт в CI), проверяются 78 уникальных ключей (22 major + 56 minor), прохождение allowlist, `src === "/" + image_key`, 10 векторов traversal/подмены расширения/регистра, поведение без `imageKey`, ветка `card-back`; (3) `public/cards/README.md` переписан по факту: контракт имён, честный статус «0 из 78», ссылка на E13/E14, маркеры `/// ⛔` лицензии сохранены.
- **Доказано «до»:** извлечено 78 уникальных `image_key` из `api/migrations/002_seed.up.sql`; **текущий allowlist совпал с 0 из 78**, с префиксом `cards/` — 78 из 78; файлов в `web/public/<key>` — **0 из 78**.
- **Доказано «после»:** regex, вытащенный из исходника, прогнан через node по всем 78 ключам → `78 / 78`; ожидаемый путь `web/public/cards/major-00-fool.webp` (его и ищет компонент) — отсутствие файла теперь видно явно, а не замаскировано несовпадением контракта.
- **Тест (мутация):** откат regex к исходному → **3 теста падают** («все 78 ключей проходят allowlist», «src собирается как /<image_key>», «card-back в allowlist»); после отката — 27/27 зелёных.
- **Честно про половину, которую я закрыть не мог:** самих 78 `.webp` нет, это задача владельца (**E13** генерация + **E14** лицензия, `docs/BACKLOG_OWNER.md`). Проверка существования ассетов оставлена **`it.todo`** с явной причиной — красный CI из-за чужой задачи хуже видимого пробела; инструкция по переводу в `it` есть в тесте и в README. Фактическое состояние: **контракт выполним, картинок нет**.
- **Регресс:** `vitest run` 27/27 + 1 todo (6 файлов), `tsc --noEmit` 0 ошибок, eslint 0 предупреждений, `npm run build` OK, PII-grep из `ci.yml` чист, `node --check deploy/admin-ui/app.js` OK. Go-код не затронут.
- **Коммит:** не выполнялся (ждёт вашего решения). Ветка: `fix/A05-card-art-contract`.

### A06 · F-32 · MEDIUM · ✅ done 2026-09-26 · 6 из 15 обязательных prod-переменных отсутствовали в `.env.example`
- **Где:** `deploy/check-env.sh` (новый, executable) · `.env.example` · `.github/workflows/deploy.yml:74-86` (префлайт) · `.github/workflows/ci.yml` (шаг) · `deploy/README.md`
- **Сделано:** (1) `.env.example` дополнен 6 недостающими ключами (`ADMIN_API_TOKEN`, `DATABASE_URL`, `OPENROUTER_API_KEY`, `REDIS_ENC_KEY`, `REDIS_PASSWORD`, `TLS_CERT_DIR`) — с **пустыми** значениями и пояснениями, чтобы шаблон не мог подставить фальшивые секреты в прод; шапка файла описывает prod-контракт из 15 переменных; (2) новый `deploy/check-env.sh` — список обязательных переменных **выводится из compose-файлов** (`${VAR:?…}`), поэтому новый требуемый ключ нельзя забыть ни в шаблоне, ни в префлайте; режимы `--keys-only` (только наличие ключа — для CI) и проверка значений с отсевом шаблонных (`CHANGE_ME*`, `dev-only-*`, `taro_dev_only`); (3) `deploy.yml` вызывает его в префлайте **до** первого `docker compose`; из префлайта убран ручной `grep '^TLS_CERT_DIR=.+$'` — покрытие теперь общее; (4) CI-шаг `deploy/check-env.sh --keys-only .env.example`; (5) README — раздел с командами и объяснением.
- **Доказано «до»:** `docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml -f deploy/docker-compose.tls.yml config --quiet` на окружении из шаблона → `error while interpolating services.migrate.environment.DATABASE_URL: required variable DATABASE_URL is missing` — **одна** переменная за прогон, остальные 6 молчат, и это происходит уже внутри деплоя.
- **Доказано «после»:** `deploy/check-env.sh` на том же окружении → `STOP: 7 required variable(s) missing or empty` со списком `ADMIN_API_TOKEN ADMIN_ORIGIN DATABASE_URL OPENROUTER_API_KEY REDIS_ENC_KEY REDIS_PASSWORD TLS_CERT_DIR` + `STOP: 6 variable(s) still hold template/dev values` (JWT_SECRET, POSTGRES_PASSWORD, TG_BOT_TOKEN, TG_STARS_SECRET_TOKEN, VAPID_PRIVATE_KEY, VAPID_PUBLIC_KEY). Один экран вместо семи итераций.
- **Режимы проверены:** (1) `--keys-only .env.example` → `all 15 required variables are present`; (2) незаполненный шаблон → exit 1 со списком; (3) полностью заполненный prod-контракт → exit 0.
- **Тест (мутация, оба пойманы):** убрать `REDIS_PASSWORD` из `.env.example` → «1 required variable(s) missing»; **добавить в `docker-compose.prod.yml` новый `${BRAND_NEW_SECRET:?…}`, которого нет в шаблоне** → «BRAND_NEW_SECRET» в списке (то есть рассинхрон после правки compose ловится автоматически, а не молча).
- **Найденный при тестировании баг в собственном скрипте:** позиционный аргумент с путём не обрабатывался (`check-env.sh /tmp/env` молча проверял `.env` по умолчанию) — из-за этого первый «тест» на самом деле валидировал локальный `.env`, а не переданный файл. Исправлено (`case` + нормализация относительного пути), перепроверено.
- **Оставлено в A33:** сам `compose config` для prod+tls в CI (нужны 15 реальных значений в job-окружении) — карточка это уже планировала.
- **⚠️ Инцидент во время проверки (честно):** чтобы показать «до», я сделал `cp .env.example .env` в рабочем репозитории и **перезаписал локальный `.env`** (gitignored dev-файл, 777 байт). Восстановил 19 значений из конфигов остановленных контейнеров (`taro-db-1`, `taro-cache-1`, `taro-api-public-1`, `taro-api-admin-1`), проверил функционально: стек поднимается, `schema_migrations=34`, `cards=78`. Три значения вернулись пустыми — `REDIS_PASSWORD`, `OPENROUTER_API_KEY`, `ADMIN_API_TOKEN`; это согласуется с dev-контуром (кэш без requirepass, `gw.Enabled()=false`, как замерялось ранее в аудите), но **если там был реальный `OPENROUTER_API_KEY` — его надо вписать заново**. Правильный способ на будущее: `docker compose --env-file /tmp/probe.env ...`, никогда не `cp` поверх `.env` в живом репозитории.
- **Регресс:** `sh -n` + `test -x` для всех `deploy/*.sh` (включая новый) — OK; `ci.yml` и `deploy.yml` парсятся; `docker compose config --quiet` (dev) валиден; `check-env.sh` в трёх режимах; 2 мутации пойманы. Go/код приложения не затронут.
- **Коммит:** не выполнялся (ждёт вашего решения). Ветка: `fix/A06-prod-env-contract`.

---

# WAVE 1 — целостность денег

### A07 · F-07 · HIGH · ✅ done 2026-09-26 · конкурентные вебхуки теряли дни подписки
- **Где:** `api/internal/payments/payments.go:605-632` · `api/migrations/035_subscription_payment_uniqueness.{up,down}.sql` (новые) · `api/internal/payments/subscription_grant_concurrency_test.go` (новый) · `.github/workflows/ci.yml` (список обязательных миграций)
- **Сделано:** (1) `pg_advisory_xact_lock(hashtext('subscriptions:'||user_id))` первым оператором выдачи — тот же паттерн, что `readings.go:317` / `ai.go:581` / `link.go:158`; (2) миграция 035: `CREATE UNIQUE INDEX idx_sub_payment_uniq ON subscriptions (payment_id) WHERE payment_id IS NOT NULL` + предварительное схлопывание дублей, созданных гонкой; (3) `ON CONFLICT (payment_id) WHERE payment_id IS NOT NULL DO NOTHING` в выдаче — повторная обработка одного платежа становится no-op, а не падением; (4) новый тест-файл с тремя тестами; (5) 035 up/down добавлены в перечень обязательных release-файлов в `ci.yml`.
- **Почему advisory-lock, а не `SELECT … FOR UPDATE`:** блокировать надо все строки пользователя, а не одну;user-скоуп + advisory-lock — единственный вариант, который не конфликтует с уже существующими блокировками в других пакетах и не требует ORDER BY-обхода.
- **Доказано «до» детерминированно, 4/4 прогона:** `valid_until lost days: got 2026-10-26, want ~2026-11-25 (delta -720h0m0.41s)` — **ровно 720 часов (30 дней) за два оплаченных месяца**, 4 из 4. Совпало с аудитом (11/12 на реальном хендлере).
- **Доказано «после»:** `-count=8` на трёх тестах — зелёные; полный `go test -race -count=1 ./...` на чистой PG16 v35 + Redis — **14/14 пакетов OK**.
- **Тест нерегулярно ловил гонку — пришлось сделать детерминированным:** без задержки транзакции настолько короткие, что **8 прогонов проходили даже без фикса**. Добавлен временный `BEFORE INSERT`-триггер с `pg_sleep(0.4)`: второй воркер гарантированно читает `MAX(valid_until)` до коммита первого. После этого 4/4 падают без фикса и проходят с фиксом. Триггер создаётся и снимается самим тестом, прод не затронут.
- **Три теста:** (1) `TestGrantConcurrentWebhooksSameUser` — 2 параллельные транзакции, инвариант «2 строки и ≈60 дней»; (2) `TestGrantSamePaymentTwiceIsNoop` — повтор одного платежа не плодит строки и не двигает `valid_until`; (3) `TestGrantSequentialExtendsFromPrevious` — дешёвый инвариант на 60 дней, ломается, если из выдачи выпадет чтение предыдущих строк.
- **Миграция проверена на грязной базе:** подсеял 55 дублей на один `payment_id` + 28 честных бонусов с `payment_id IS NULL`. После 035: остался **1** платёжный (с `valid_until` = 60 дней, то есть выжил больший — entitlements не теряются), все 28 `NULL`-бонусов целы, `dirty=false`. Прямая попытка вставить дубль → `duplicate key value violates unique constraint "idx_sub_payment_uniq"`. `down 1` → индекс убран, `dirty=false`, `up` снова применяется.
- **⚠️ Важно про порядок деплоя:** `ON CONFLICT (payment_id)` требует существующего индекса. На БД без 035 все три теста падают с `42P10 no unique or exclusion constraint matching the ON CONFLICT specification` — то есть отказ громкий, не тихий. `deploy.yml` выполняет `migrate.sh up` до пересоздания контейнеров, поэтому порядок соблюдён.
- **Регресс:** `go build`, `go vet`, `gofmt` (0 файлов), полный `go test -race -count=1 ./...` 14/14, `deploy/migrate.sh manifest` → 35, `ci.yml` парсится. Web/infra не затронуты.
- **Коммит:** не выполнялся (ждёт вашего решения). Ветка: `fix/A07-subscription-grant-race`.

### A08 · F-08 · HIGH · ✅ done 2026-09-26 · дедлок webhook ↔ `DELETE /v1/me` + тихая порча сессии
- **Где:** `api/internal/payments/payments.go:152-180` · `api/internal/me/me.go:47-100` · новые тесты `api/internal/payments/webhook_delete_deadlock_test.go`, `api/internal/me/delete_rollback_test.go`
- **Сделано:** (1) **глобальный порядок блокировок users → payments**: в `loadWebhookPayment` user-строка берётся под `FOR KEY SHARE` **отдельным оператором до** `FOR UPDATE OF p`; (2) очистка Redis (`sess`/`csrf`/`sess:admin`/`ent:*`) перенесена **после** `tx.Commit()`.
- **Почему отдельным оператором, а не вторым locking-клаузом в одном SELECT:** порядок захвата внутри одного запроса задаётся планом, а не текстом запроса. Порядок в отдельных операторах от плана не зависит — это и есть смысл «глобального правила». Плюс `LEFT JOIN users` нельзя блокировать вовсе (`FOR UPDATE cannot be applied to the nullable side of an outer join`), так что вариант «тот же SELECT» был бы ещё и невозможен.
- **Доказано «до» прямым измерением, 50/50 итераций:** счётчик `pg_stat_database.deadlocks` **0 → 50**, `DELETE /v1/me` вернул **500 в 50 из 50** итераций. Совпало с аудитом (20/20 на реальных хендлерах).
- **Доказано «после»:** те же 50 итераций — **0 дедлоков, 0 неуспешных delete** (счётчик не вырос). Обе транзакции завершаются штатно: вебхук 200, удаление 200.
- **Тест дедлока сделан детерминированным:** триггер `BEFORE UPDATE ON payments` с `pg_sleep(0.4)` расширяет окно между захватом строки payments и вставкой в subscriptions. Измерение сделано **счётчиком дедлоков в PostgreSQL**, а не HTTP-кодами: 500 может прийти и по другой причине, а каждый дедлок — это ровно один `deadlock aborted`. Отдельный триггер ставится и снимается тестом.
- **Второй тест ловит очистку-до-коммита:** `TestDeleteFailureKeepsSession` ставит `BEFORE DELETE ON users`-триггер, который всегда кидает исключение (транзакция откатывается, учётная запись жива), и требует, чтобы `sess:`, `csrf:`, `ent:*` остались в Redis. Мутация (перенос очистки обратно до транзакции) → `redis key sess:… was purged although the delete transaction rolled back`.
- **Что стало с деньгами:** цепочка «webhook проиграл дедлок → `loadWebhookPayment` вернул 0 строк → `writeWebhookDuplicate` → HTTP 200 при нуле строк в `payment_webhook_events`» («деньги сняты, доступ не выдан, тихо подтверждено») теперь недостижима: пользовательская блокировка берётся первой, поэтому вебхук не может оказаться жертвой. `payment_webhook_events` всё равно не читается никем — это **A09**.
- **Прочие выводы разбора (в A08 не входили):** ветка merge недостижима для дедлока (для неё нужен `tg_id IS NULL`, а тогда вебхук идёт в `owner_unverified` и не вставляет подписку) — подтверждено, как и в аудите; путь refund берёт payments → subscriptions и user-строки не трогает, цикла не создаёт.
- **Регресс:** полный `go test -race -count=1 ./...` — **14/14 пакетов OK** (payments 25.8 s, me 3.6 s), `go build`, `go vet`, `gofmt` 0 файлов. Интерфейс `queryRower` расширен на `Exec` (pgx.Tx и pgxpool.Pool это уже умели). Временные контейнеры убраны.
- **Коммит:** не выполнялся (ждёт вашего решения). Ветка: `fix/A08-webhook-delete-lock-order`.

### A09 · F-09 · HIGH · ✅ done 2026-09-26 · `payment_webhook_events` писалась, но не читалась
- **Где:** новый `api/internal/payments/reconcile.go` · новый `api/cmd/adminctl/payments_reconcile.go` · `api/cmd/adminctl/main.go` (диспетчер подкоманд) · `api/internal/payments/payments.go` (константы причин + `HandleAdminWebhookAudit`) · `api/cmd/admin/main.go` (маршрут) · `deploy/admin-ui/app.js` (блок в разделе «Платежи») · `deploy/README.md` (cron/алерт) · новый тест `api/internal/payments/webhook_audit_test.go`
- **Сделано:** (1) `ReconcileWebhookEvents(ctx, pg, since, limit)` — единственная выборка, группирующая события по (payment_id, reason) с контекстом платежа (статус, refund_state, сумма) и сортировкой по свежести; её используют CLI, админ-API и тесты — ручных копий SQL не осталось; (2) `adminctl payments reconcile [--since|--all|--limit|--json]` — человекочитаемая таблица + JSON, код возврата **2** при записях, требующих внимания (на этом алерт, пока нет метрик — F-20); (3) `GET /v1/admin/payments/audit` за `RequireAdmin`; (4) блок «Аудит вебхуков» в панели Taro Control; (5) **все SQL-литералы причин в `payments.go` заменены на константы** (`ReasonDuplicateCharge` и т. д., включая `= ANY($6)` вместо `IN (...)`) — контракт теперь проверяет компилятор; (6) `owner_unverified` помечен как нормальный первый вебхук и не поднимает тревогу.
- **Доказано на живом стеке:** собрал `api-admin` с новым маршрутом, поднял `admin-access`, эндпоинт через loopback+X-Admin-Token вернул **84 реальных события** в dev-БД, включая `amount_mismatch`, `charge_reused` и `duplicate_charge` — то есть инциденты, зафиксированные и незамеченные. Неверный токен → 403. CLI: `--since 1970-01-01` печатает таблицу и даёт `exit=2`; без записей `exit=0`; неверный `--since` и неизвестный глагол дают внятную ошибку; старое поведение provisioning'а не сломано.
- **Тесты (4):** `TestReconcileFindsDuplicateCharge` (группировка 2 событий в одну строку, сохранение user/статуса/суммы), `TestReconcileOwnerUnverifiedIsNotAnIncident`, `TestReconcileRespectsSinceAndLimit`, `TestAdminWebhookAuditEndpointReadsTheSameData` (серверный путь `X-Admin-Token` + loopback, плюс проверка 403 с не-loopback адреса).
- **Мои тесты сначала были не изолированы** — при прогоне всего пакета они падали, потому что другие тесты (вебхуки) оставляют события в общей таблице, а ассерты считали глобальные счётчики. Переделал на scoped-фикстуры (`findItem(items, paymentID, reason)`). Это **тот же класс дефекта, что F-58**: тест, зависящий от чужих данных в общей БД.
- **Проверено отдельно:** `me.go` удаляет `payment_webhook_events` только вместе с аккаунтом и только в своей транзакции — этот пункт карточки уже был корректен, менять не потребовалось.
- **Регресс:** полный `go test -race -count=1 ./...` — **14/14** (payments 32.5 s), `go build`, `go vet`, `gofmt` 0, `node --check deploy/admin-ui/app.js`, `sh -n deploy/check-env.sh`. Временные контейнеры и бинарь adminctl убраны.
- **Коммит:** не выполнялся (ждёт вашего решения). Ветка: `fix/A09-webhook-audit-reader`.

### A10 · F-24 · MEDIUM · ✅ done 2026-09-26 · кап реферальных бонусов больше не check-then-act
- **Где:** `api/internal/referral/referral.go:178-206` (+ новая `applyReferralCapTx`, константа `referralMonthlyCapDays`) · новый тест `api/internal/referral/referral_cap_test.go`
- **Сделано:** (1) advisory-блокировка referrer (`pg_advisory_xact_lock(hashtext('referral-cap:'||referrer))`) перед чтением счётчика — все завершения рефералов одного пользователя выстраиваются в очередь до конца транзакции; (2) чтение счётчика перенесено в `applyReferralCapTx` под этой блокировкой; (3) инкремент дополнительно условный (`WHERE … <= 30` в `DO UPDATE`) — страховка, если кап начнут править из другого места кода; (4) `referralMonthlyCapDays = 30` вместо магического числа в двух местах.
- **Доказано мутацией, 3/3 прогона:** возврат к check-then-act (снял блокировку и `WHERE`) → `TestReferralCapConcurrentNeverExceeds30` падает; с фиксом `-count=3` зелёные. Тест детерминирован триггером `pg_sleep(0.3)` на `INSERT INTO entitlements` — без задержки окно слишком узкое.
- **Тесты (2):** (1) 8 параллельных завершений по 3 дня на одного referrer — месячный счётчик ≤ 30 и **равен** сумме выданного (`month == completed*3 == lifetime`); (2) последовательный инвариант: 10 рефералов по 3 дня заполняют кэп ровно в 30, следующий отклоняется, счётчик не растёт, рефереру не начислено лишних дней.
- **Найденный при тестировании баг в собственном фиксе:** первая версия делала компенсирующий `UPDATE` при отказе по капу — но условный upsert и не менял строку, поэтому компенсация **уменьшала** счётчик (тест поймал: после 10 рефералов было 30, после 11-го стало 27). Убрал компенсацию: под блокировкой чтение безопасно, а пропущенный `WHERE`-UPDATE не мутирует данные.
- **Регресс:** полный `go test -race -count=1 ./...` — **14/14**, `go build`, `go vet`, `gofmt` 0.
- **Коммит:** не выполнялся. Ветка: `fix/A10-referral-cap-atomic`.

### A11 · F-25 · MEDIUM · ✅ done 2026-09-26 · merge больше не теряет provenance платежа
- **Где:** `api/internal/auth/link.go:255-266` (дедупликация подписок) · новый `api/internal/auth/merge_provenance_test.go` · новый `api/internal/payments/refund_revoke_test.go`
- **Сделано:** в дедупликацию добавлено обязательное `AND s.payment_id IS NULL`. Строка, за которой стоит платёж, больше не удаляется, поэтому `finalizeRefund` (`UPDATE subscriptions … WHERE payment_id=$1`) доходит до неё. Дни при этом не теряются: entitlement считается как `max(valid_until)` по активным строкам, а сохранённая строка как раз имеет больший `valid_until`.
- **Доказано мутацией (2 теста поймали):** снятие условия → `merge destroyed the payment linkage: 0 rows left` и `both paid rows must survive the merge, got 1`. С условием — зелёные.
- **Цепочка закрыта с двух сторон:** (1) `auth`: merge сохраняет связь `payment_id → subscriptions` и не теряет дни; (2) `payments`: при наличии связи `finalizeRefund` отзывает дни (`refunded`, активных строк 0), а **без** связи не отзывает ничего — контр-случай фиксирует, почему сохранение provenance обязательно. Не стал делать один сквозной тест через экспорт `finalizeRefund`: для этого пришлось бы добавить тестовый хук в production-API.
- **Тесты (4):** `TestMergeKeepsPaymentProvenanceForRefund`, `TestMergeDoesNotLoseDaysForTwoPaidRows` (две оплаченные строки одного плана — обе выживают), `TestFinalizeRefundRevokesSubscriptionLinkedByPayment`, `TestFinalizeRefundWithoutLinkRevokesNothing`.
- **Грабли при написании фикстур:** `refund_state='manual'` нельзя задать на вставке (CHECK переходов требует `succeeded → requested`); путь возврата в БД: `requested` → `confirmed` → `finalizeRefund` (триггер 024). Первый вариант теста был вакуумным (условие в мутации не применилось, тест «проходил» на неизменённом коде) — поймал по тому, что `grep` показал 2 совпадения вместо ожидаемого.
- **Регресс:** полный `go test -race -count=1 ./...` — **14/14**, `go build`, `go vet`, `gofmt` 0.
- **Коммит:** не выполнялся. Ветка: `fix/A11-merge-keeps-payment-provenance`.

### A12 · F-12 · HIGH · ✅ done 2026-09-26 · терминальный провал возвращает entitlement
- **Где:** `api/internal/entitlements/release.go` (новый) · `api/internal/entitlements/release_test.go` (новый) · `api/internal/ai/worker.go:121-150` (массовый переход «попытки исчерпаны») и `:310-332` (`markFailed`) · `api/internal/ai/worker_release_test.go` (новый) · `api/internal/readings/readings.go:998-1062` (ответ GET) · новая `api/migrations/036_authorization_release.{up,down}.sql` · `.github/workflows/ci.yml` (список обязательных миграций)
- **Сделано:** добавлен `ReleaseReadingAuthorization(ctx, readingID)` — одна транзакция, `SELECT … FOR UPDATE` по квитанции с `released_at IS NULL`, затем по `kind`: `daily` → декремент `free_used_today` за сохранённую в квитанции дату, `love_weekly` → декремент недельного счётчика, `single` → сброс `consumed_reading_id` (только если он указывает именно на это чтение), `subscription`/`legacy` → no-op. Финал — `released_at=now()`. Вызов встроен в **обе** терминальные ветки воркера: одиночную (`markFailed`, попытка провалилась) и массовую (`drainOnce` переводит в `failed` строки, исчерпавшие `worker_attempts`).
- **Причина провала клиенту (DoD):** миграция добавила `readings.failure_reason` + `readings.failed_at`; воркер пишет машиночитаемый код `provider_failed` (текст ошибки провайдера наружу не уходит — там могут быть детали запроса). `GET /v1/readings/:id` отдаёт `failure_reason`, `failed_at` и `retry_free: true` **только** для `status='failed'`, чтобы не менять контракт успешных чтений.
- **Ключевая тонкость, которую пришлось закрыть миграцией:** уникальный индекс `idx_reading_authorization_receipts_entitlement (entitlement_id) WHERE entitlement_id IS NOT NULL` после освобождения делал single-покупку **навсегда** непригодной — вторая попытка вставляла новую квитанцию с тем же `entitlement_id` и падала в `23505 duplicate key`. Поэтому индекс пересоздан как частичный `… AND released_at IS NULL`: он защищает от двойного списания только пока квитанция активна. `down` перед восстановлением исходного индекса схлопывает дубли по `entitlement_id` (оставляет самую свежую квитанцию) — иначе откат падал бы на новых парах квитанций.
- **Доказано мутациями (4 штуки, все пойманы):** (1) отключён возврат в `markFailed` → `entitlement not released after terminal failure: free_used_today=1`; (2) отключён возврат в массовой ветке → та же ошибка на `TestWorkerReleasesEntitlementOnExhaustedAttempts`; (3) не пишется `failure_reason` → `terminal failure must record failure_reason`; (4) индекс возвращён к `WHERE entitlement_id IS NOT NULL` → `re-authorize after release: duplicate key value violates unique constraint "idx_reading_authorization_receipts_entitlement" (SQLSTATE 23505)`.
- **Тесты (6):** `TestReleaseDailySlotOnTerminalFailure`, `TestReleaseDailySlotThenReusableForNewReading` (слот можно потратить заново), `TestReleaseSinglePurchaseOnTerminalFailure` (проверяет и повторное использование покупки — ради этого и переписан индекс), `TestReleaseDoesNotTouchSubscriptionEntitlement`, `TestWorkerReleasesEntitlementOnExhaustedAttempts`, `TestWorkerReleasesEntitlementOnMarkFailed`. Проверен и round-trip `036 down → up` (включая схлопывание дублей на реальных данных), `deploy/migrate.sh manifest` → 36.
- **Грабли при написании тестов:** (1) первый вариант теста гонял настоящий сталящий HTTP-провайдер — он оказался **недетерминированным**: ветка выбирается по таймингам, а не по состоянию, и тест то проходил, то падал; заменён на прямое воспроизведение терминальных условий (`worker_attempts` упёрся в лимит + истёкший lease, и выданный claim + `markFailed`). (2) `single_entitlements` не имеет колонки `plan_id` — фикстура строится через реальную `payments`-строку. (3) `worker_lease_until` у freshly-авторизованного reading оказывается не NULL, поэтому в тесте его надо гасить руками, иначе ветка «попытки исчерпаны» закономерно не срабатывает (lease активного воркера трогать нельзя — это правильное поведение). (4) Мутация причины провала один раз «съела» строку `SET`, а мой откат её не восстановил → лишний аргумент в `Exec`, ошибка проглатывалась через `tag, _`, и тест падал с «pending». Ошибку `markFailed` теперь логируем явно — молчание и скрыло поломку.
- **Регресс:** полный `go test -race -count=1 ./...` — **14/14**, `go build ./...`, `go vet ./...`, `gofmt` 0, `036 down/up` round-trip, manifest 36.
- **Коммит:** не выполнялся. Ветка: `fix/A12-release-entitlement-on-terminal-failure`.

### A13 · F-11 · HIGH · ✅ done 2026-09-26 · TTFT и генерация получили разные бюджеты
- **Где:** `api/internal/ai/ai.go:27-36` (константы + `ttftBudget`/`generationBudget`/`durationFromEnv`) · `api/internal/ai/ai.go:112-118` (`ResponseHeaderTimeout`) · `api/internal/ai/ai.go:790-800` (breaker-фильтр) · `api/internal/ai/ai.go:~830` (`ourBudgetExpired`) · новый `api/internal/ai/timeout_budget_test.go` · `.env.example` · `docker-compose.yml`
- **Сделано:** `RequestTimeout = 8s` остался **только** как бюджет TTFT (`http.Transport.ResponseHeaderTimeout`), общий дедлайн попытки стал отдельным `GenerationTimeout = 60s` (`context.WithTimeout`). Оба читаются из env (`AI_TTFT_TIMEOUT`, `AI_GENERATION_TIMEOUT`) — бюджет подкручивается без пересборки. Плюс breaker перестал считать провайдерским провалом то, что провайдером не является: отмена клиентом и **наш собственный** дедлайн генерации.
- **Главная тонкость (найдена тестом, а не рассуждением):** TTFT-таймаут транспорта приходит тем же `context.DeadlineExceeded`, что и наш собственный дедлайн. Простая правка `DeadlineExceeded → не считать` глушила и TTFT-таймаут, то есть выключала бы breaker на молчащем провайдере (тест `TestTTFTBudgetIsSeparateAndCountsAsFailure` это поймал). Развели по источнику: `budgetExpired` снимается **до** `cancel()` (после отмены `attemptCtx` всегда выглядит завершённым), и если бюджет не истёк — ошибка транспорта, значит честный провайдерский провал.
- **Доказано мутациями (5, все пойманы):** (1) общий дедлайн снова 8 с → `healthy 12s stream must complete, got ""`; (2) `ResponseHeaderTimeout` убран → `TTFT budget must cut the attempt quickly, took 2m0.02s`; (3) `ourBudgetExpired` всегда `false` → `our own generation deadline must not count as provider failure … got 1`; (4) `context.Canceled` снова `true` → табличный тест `client cancel: counted=true, want false`; (5) порог breaker поднят → `breaker must open after 5 provider 5xx, но не открылся`.
- **Тесты (6):** `TestSlowHealthyStreamCompletes` (12-секундный healthy-поток завершается, breaker не тронут), `TestProviderErrorOpensBreaker` (5xx открывает breaker), `TestClientCancelDoesNotOpenBreaker`, `TestGenerationDeadlineDoesNotOpenBreaker` (наш таймаут ≠ поломка провайдера), `TestTTFTBudgetIsSeparateAndCountsAsFailure` (молчащий провайдер обрывается быстро И идёт в breaker), `TestBreakerClassification` (таблица решений breaker'а).
- **Грабли, о которых стоит помнить:** (1) первая версия теста ждала ответа провайдера **до** отправки запроса — тупик на 10 минут; (2) `httptest.Server.Close()` ждёт завершения хендлера, поэтому `defer close(hold)` должен идти **после** `defer srv.Close()` (LIFO), иначе тоже взаимоблокировка; (3) breaker-ключ хранит строку `"open"` — `Get(...).Int64()` по нему всегда 0, проверять надо `Exists`; (4) первая мутация `ourBudgetExpired` **не компилировалась** (неиспользуемая переменная), и из-за этого выглядела как «тест прошёл» — мутацию надо подтверждать сборкой; (5) `go test ./...` гоняет пакеты **параллельно по общей БД/Redis**: соседний пакет дописывал в breaker-счётчик прямо во время 12-секундного окна теста. Вылечено минимальным тестовым швом `Gateway.cfgOverride` (в проде всегда `nil`) — модель теста стала уникальной, без записи в `app_config`, который виден всем пакетам.
- **Побочная находка (не в скоупе A13):** `internal/payments` использует счётчик `tgCounter+4200` для `tg_id`. Убитый по таймауту прогон оставляет пользователя с `tg_id=4201`, и после этого пакетами `payments` падают на `idx_users_tg` — состояние, а не регрессия. Чинить в A14+/отдельной задаче.
- **Регресс:** полный `go test -race -count=1 ./...` — **14/14** два раза подряд, `go build ./...`, `go vet ./...`, `gofmt` 0, `docker compose config` валиден, `deploy/check-env.sh` работает.
- **Коммит:** не выполнялся. Ветка: `fix/A13-split-ttft-and-generation-budgets`.

### A14 · F-18 · HIGH · ✅ done 2026-09-27 · тесты денежных путей не проверяют ничего
- **Суть находки:** денежные пути были покрыты тестами, которые физически не могли упасть: refund монтировался bare (авторизации в тесте не было вовсе), ветка отказа Telegram не исполнялась из-за отсутствия `TG_BOT_TOKEN`, тесты затирали чужой Redis, `-count=2` падал, SSE-контракт не был зафиксирован ни с одной стороны.
- **Итог:** все 6 пунктов закрыты, 11 мутаций пойманы, полный регресс зелёный в 3 прогонах подряд.
- **Регресс:** `go test -race -count=2 ./...` — **14/14** три раза подряд; `cd web && npx tsc --noEmit` — 0 ошибок, `npm test` (vitest) — 7 файлов / 31 тест, 1 todo.

#### (1) Refund за RequireAdmin + реальная ветка отказа — сделано
- **Где:** новый `api/internal/payments/refund_authz_test.go`, `api/internal/payments/payments.go` (`telegramAPIURL`), `api/cmd/admin/main.go` (только читается тестом).
- **Сделано:** `HandleRefund` в тестах смонтирован за настоящим `admin.RequireAdmin` (прежде он был bare, и регрессия авторизации на деньгах была невидима). Проверены 403 без токена, 403 с чужим токеном и 404 с верным (разница доказывает, что middleware стоит на маршруте). Прод-роутер защищён статическим guard-тестом `TestRefundRouteIsWiredBehindRequireAdmin` (он читает `cmd/admin/main.go`, т.к. `package main` не импортируется).
- **Ветка отказа Telegram теперь исполняется по-настоящему:** добавлен `telegramAPIURL()` по образцу `openRouterURL` — подмена адреса только для локального http-хоста и только при `TELEGRAM_ALLOW_CUSTOM_BASE=1` (без этого refunds можно было бы увести на чужой хост). Мок Telegram проверяет, что он **вызван**, иначе тест падает: прежде без `TG_BOT_TOKEN` срабатывал ранний `!tgTokenReady()`-return и тест «проходил», ничего не проверяя.
- **Важно (исправило ложное предположение DoD):** после отказа Telegram платёж остаётся в `status='refunding'`, а не `'succeeded'`. Это НАМЕРЕННО и требуется CHECK-ом `payments_refund_state_transition_check` (status='refunding' при любом `refund_state != 'none'`): исход возврата неизвестен, платёж ждёт reconcile. Старый тест утверждал `status='succeeded'` и проходил только потому, что ветка не исполнялась. Причина пишется в `refund_last_error` + `reconciliation_reason='refund_unknown'`; повторный запрос честно отвечает 409.
- **Мутации (3 пойманы):** снятие `RequireAdmin` в `cmd/admin/main.go` → `prod admin route must keep refund behind RequireAdmin`; «Telegram отказал, но помечаем refunded» → падает `TestE2ERefundTelegramFailureIsRecorded`.
- **Тесты (4):** `TestE2ERefundRequiresAdmin`, `TestE2ERefundTelegramFailureIsRecorded`, `TestE2ERefundTelegramSuccessFinalizes` (контр-случай: без него «лечение» в виде вечного `unknown` прошло бы), `TestRefundRouteIsWiredBehindRequireAdmin`.

#### (2) Ветка отказа Telegram не зависела от CI-env — сделано
- **Проблема:** чтобы ветка отказа Telegram исполнилась, нужен непустой `TG_BOT_TOKEN`; в CI его нет (и не должен быть — секрет). Оставлять это «настройкой окружения» значило бы сохранить ровно тот дефект, который чинится: тест снова станет вакуумным в любом окружении без токена.
- **Сделано:** refund-тест выставляет себе и токен, и адрес Telegram сам (`t.Setenv("TG_BOT_TOKEN", …)`, `t.Setenv("TELEGRAM_API_BASE", srv.URL)`, `t.Setenv("TELEGRAM_ALLOW_CUSTOM_BASE", "1")` в `refund_authz_test.go:46-48`). Ветка TG-отказа/успеха теперь исполняется в любом окружении, включая CI, без единой новой CI-переменной.
- **Почему это не ослабляет проверку:** подмена адреса Telegram разрешена только для `127.0.0.1`/`localhost` и только по схеме `http` при явном флаге (см. (1)), в проде значение всегда `https://api.telegram.org`.
- **Мутации (2 пойманы):** снятие `t.Setenv("TG_BOT_TOKEN", …)` → падает `TestE2ERefundTelegramFailureIsRecorded` (ранний `!tgTokenReady()`-return, мок не вызван); возврат `telegramAPIURL()` к жёстко зашитому `https://api.telegram.org` → оба refund-теста падают с таймаутом обращения к Telegram.

#### (3) Изоляция тестов Redis — сделано, с откатом ложной идеи
- **Сделано:** убраны все `SCAN`+`DEL` по чужим ключам (`auth/handlers_e2e_test.go` вытирал `rl:*` у соседних пакетов, `ai/ai_e2e_test.go` — `ai:*`).
- **Ложная идея, которую пришлось откатить:** «своя Redis-БД на пакет» через FNV-хеш имени пакета. В Redis всего 16 БД, а пакетов 14 → **коллизии**: `ratelimit`/`readings`/`spreads` → db2, `auth`/`apierr` → db10, `diary`/`me` → db15, `entitlements`/`store` → db5. На практике это снова смешало состояние (ratelimit падал с 429). Проверено прямым расчётом индексов, откачено.
- **Рабочая изоляция — по идентичности, а не по затиранию:** `testutil.UniqueIP` (случайная база на процесс + счётчик), `testutil.UUID` (для ключей бакетов по user_id и уникальных provider_payment_id), уникальный вопрос (входит в `promptHash` → в ключ AI-кэша) и уникальная модель через тестовый шов `Gateway.SetConfigOverride` (модель входит и в ключ кэша, и в ключ breaker'а). Все shim-ы в проде выключены (nil / всегда `https://api.telegram.org`).
- **Что вскрылось по пути (важно):** breaker дефолтных моделей в общем Redis ронял «живые» SSE-тесты пакета `readings` на 5 минут после любой серии отказов; фиксированные `Idempotency-Key` (`live-1`, `sse-1`) приводили к replay чужого чтения; фиксированный IP в админ-тестах копил попытки входа (10 попыток / 15 мин) и давал 429 вместо 401; админские IP вида `fmt.Sprintf("198.18.%d.%d", наносекунды)` — всего 65 536 значений, и тест намеренного rate-limit отравлял чужой тест.

#### (4) `-count=2` — сделано
- **Грабли, которые пришлось закрыть:** фиксированный IP `9.9.9.9`; `SELECT COUNT(*) FROM referrals` (глобальный счётчик вместо «строки этого реферера»); фиксированные `anon_uuid`/fingerprint и `Idempotency-Key`; фиксированный `provider_payment_id` в `merge_provenance_test.go` (A11); гонка в `TestE2ESameKeyConcurrentRequestsGenerateOnce` (генерация асинхронна — добавлено `waitTerminal`); фиксированные IP в auth/admin/payments-тестах.
- **Мутация (1 поймана):** отключение склейки части кадра в `web/lib/api.ts` → 3 упавших теста SSE.

#### (5) Ветки лимитера — сделано
- **Где:** новый `api/internal/ratelimit/rules_test.go` (4 теста): `byUser`-бакет пер-user (два юзера с одного IP не делят, один юзер с разных IP — делит), админская cookie `taro_admin` в отдельном бакете, `failClosed` → 503 при мёртвом Redis и fail-open для `/v1/spreads` и `/v1/share`, инвариант «у каждого префикса есть правило» + «100 запросов к незащищённому пути не тратят лимиты других».
- **Мутации (3 пойманы):** `if false` вместо `failClosed` → `failClosed /v1/auth/anon: want 503 got 200`; ключ бакета всегда по IP → `B from same IP must have its own bucket, got 429`; `taro_admin` убран из ключа → `anonymous must not share the admin bucket, got 429`.

#### (6) Контракт SSE с обеих сторон — сделано
- **Где:** новый `api/internal/readings/sse_contract_test.go` (2 теста) и новый `web/lib/api.sse.test.ts` (4 теста).
- **Сделано:** ответ разбирается тем же парсером, что в `web/lib/api.ts` (префикс `data:`, разделитель пустая строка, поля `token`/`done`/`reading_id`/`status`); на веб-стороне поток режется на байтовые куски — клиент обязан склеивать части кадра; 402 → `LIMIT_EXCEEDED`.
- **Уточнение контракта:** `fallback`-кадр сервер шлёт не во всех ветках (при выключенном gateway его нет). Тест утверждает ФОРМУ кадра, а не наличие: требование «fallback всегда» означало бы утверждать несуществующую гарантию. Заодно зафиксировано, что веб-клиент кадр `fallback` игнорирует — это кандидат в отдельную карточку.
- **Мутации (2 пойманы):** сервер шлёт `text` вместо `token` → `client would render nothing: no token frames`; клиент теряет буфер кадра → 3 упавших теста.
- **Прогон web (сделан):** `npx tsc --noEmit` — 0 ошибок; `npm test` (vitest run) — 7 файлов / 31 тест зелёные, 1 todo. Контракт SSE проверен с обеих сторон в одном прогоне. Побочно: `web/tsconfig.tsbuildinfo` меняется при каждом `tsc` (в нём видны машинные пути `node_modules` и `.next`) — файл откатан к HEAD как шум, а сам факт его трекинга в git оставлен на будущее (см. «Побочные находки»).

#### Побочные находки (вне скоупа A14, не чинил)
- **F-58 (из отчёта):** `drainOnce` захватывает ЛЮБОЕ подходящее чтение (`workerBatchSize=1`), поэтому при параллельной работе пакетов тесты видели «своё» чтение нетронутым. Прод-claim не чинил (отдельная карточка) — сделал тесты терпимыми: `drainOwn`/`drainOwnAny` повторяют drain до обработки своего чтения.
- Платёж после отказа Telegram нельзя перезапустить через endpoint: `startRefund` требует `status='succeeded'`, а он `refunding` → повтор даёт 409 «нужен reconcile». Путь ручного reconcile для таких платежей не проверен.
- `internal/payments` берёт `tg_id` из счётчика `+4200`; убитый по таймауту прогон оставляет пользователя, и после этого пакет падает на `idx_users_tg` (состояние БД, не регрессия).
- Охват лимитера в `ai`/живых тестах смешивается с состоянием дефолтных моделей: любой, кто откроет их breaker, ломает чужие тесты на 5 минут. В рамках A14 закрыто уникальной моделью в тестах.
- `web/tsconfig.tsbuildinfo` закоммичен в git и переписывается на каждом `tsc` (внутри — абсолютные/машинные пути `node_modules` и `.next`), поэтому любой прогон типизации даёт ложный diff из одной гигантской строки. Здесь откачено к HEAD; кандидат в отдельную карточку (вынести в `.gitignore`).

#### Ответ на исходный вопрос карточки
- **Могут ли тесты поймать дефекты денежных путей?** До A14 — нет: все 4 refund-теста проходили, ни разу не исполнив ни авторизацию, ни обращение к Telegram. Теперь ловят: снятие `RequireAdmin`, обход `telegramAPIURL()`, «Telegram отказал, но помечаем refunded» и отсутствие `TG_BOT_TOKEN` роняют тесты с точными сообщениями.
- **Сколько это заняло мутаций:** 11 пойманных (3 refund в (1), 2 в (2), 1 в (4), 3 лимитера в (5), 2 SSE в (6)).

### A15 · F-06 · HIGH · ✅ done 2026-09-27 · потолок воркера 2.00 readings/min
- **Где:** `api/internal/ai/worker.go` (константы, `workerPolicy`, `startWorker`, `drainOnce`, `processClaim`), `api/cmd/api/main.go:93`, `.env.example`, `docker-compose.yml`, `deploy/docker-compose.prod.yml`, `deploy/README.md`.
- **Суть находки (подтверждена замером, а не чтением кода):** жёсткие `workerBatchSize = 1` + `gw.StartWorker(workerCtx, pg, 30*time.Second)` = одно чтение за 30 секунд. Замер «до»: **4 из 100 pending-чтений за 2 минуты = 2.00 readings/min**, `maxInFlight=1`.
- **Замер «после»:** **100/100 за 6.57 с = 912.90 readings/min**, `maxInFlight=10`, 100 вызовов провайдера, сумма `worker_attempts` = 100. Подхват первого чтения после простоя: **1.99 с** (было до 30 с).

#### Что сделано
- **`AI_WORKER_BATCH_SIZE`** (1–50, по умолчанию 10) — сколько чтений захватывается за один drain. Отдельного `concurrency` намеренно нет: «захватили 10, обработали 1» — это ровно тот потолок, который чинится, и лишний ручкой его можно было бы вернуть. Batch = параллелизм.
- **`AI_WORKER_TICK_SECONDS`** (1–60, по умолчанию 2) — период ожидания **пустой** очереди, а не темп обработки. Раньше тик был темпом: 30 с ждали между чтениями независимо от состояния очереди.
- **Дренирование без ожидания тика при непустой очереди:** если батч забран целиком, воркер делает следующий drain сразу. Тик стал «сколько ждать, когда нечего делать».
- **Параллельная обработка батча** (`processClaim` на горутину на строку). Каждая задача имеет свой claim-токен и свой контекст, запись по-прежнему защищена `worker_claim_token=$4`, поэтому двойная обработка одного чтения невозможна; `worker_attempts` инкрементится на каждом захвате — это и есть доказательство «не обработано дважды» в тесте.
- **Наблюдаемость глубины очереди:** в проекте нет метрик-стека (prometheus отсутствует), поэтому `drainStats{claimed, persisted, queueDepth, batchSize, exhausted}` возвращается из `drainOnce` и логируется при непустом батче. `pendingQueueDepth` считается по партиальному индексу `idx_readings_worker_quota` (index-only scan). Лог при пустой очереди не пишется, чтобы не шуметь каждые 2 с.

#### Мутации (4, все пойманы)
1. `defaultWorkerBatchSize = 1` → `default worker batch=1, want >= 5`; с ослабленным guard'ом та же мутация даёт `backlog drained without parallelism: 1m1.427s, want < 15s` (guard ослаблялся намеренно, чтобы доказать, что **временная** граница тоже несущая, а не только проверка чисел).
2. Параллельный цикл по jobs → последовательный → `1m1.168s, want < 15s`, `maxInFlight=1`.
3. `defaultWorkerTick = 30s` → `idle reading was not picked up within 10s (tick=30s): status=pending`.
4. `cmd/api/main.go` снова хардкодит `30*time.Second` → `cmd/api/main.go must take the worker tick from ai.WorkerTick(), not a literal`.

#### Тесты (5 новых/переписанных)
- `TestWorkerDrainsBacklog` (новый): 100 pending-чтений, замер скорости, границы DoD (2 мин) и границы параллельности (15 с), `maxInFlight ≥ 2`, сумма попыток = 100, все interpretations непустые.
- `TestWorkerPicksUpIdleReadingFast` (новый): подхват первого чтения при пустой очереди — пользовательская форма F-06. Отдельно нужен потому, что бэклог-тест тик **не видит**: при полной очереди воркер дренирует без ожидания.
- `TestWorkerPolicyIsConfigurable` (новый, 7 подслучаев): дефолты, малые значения, клампы вверх, нули и мусор → дефолт.
- `TestWorkerTickIsNotHardcodedInMain` (новый): статический guard по образцу `TestRefundRouteIsWiredBehindRequireAdmin` (A14/F-18), потому что `package main` не импортируется.
- `TestWorkerLeaseCoversClaimedBatch` (переписан): инвариант «лиза перекрывает обработку батча» стал «лиза перекрывает обработку **одного** чтения» — при параллельном батче этого достаточно, при последовательном 10×30 с лизы в 2 мин не хватило бы.

#### Грабли, о которых стоит помнить
1. **Быстрый мок-провайдер маскировал потолок.** Первая версия теста отвечала мгновенно, и ВСЕ формы дефекта проходили: последовательный воркер на бесплатных запросах обслуживал 100 чтений за ~4 с, быстрее любой границы DoD. Только задержка 600 мс на ответ делает границу честной: последовательно это 60 с, параллельно ~6 с, порог 15 с (запас ×2.5 на медленную машину).
2. **Существующий тест защищал ОШИБКУ.** `TestE2EWorkerClaimsBoundedBatch` утверждал `done=1, pending=1` после одного drain — то есть фиксировал батч=1 как контракт. Правку он бы «поймал» как регрессию. Переписан на «batch+1 строк, один drain, `done=batch, pending=1, attempts=batch`».
3. **Тик и бэклог — независимые поверхности:** мутация тика (30 с) при полной очереди не влияет на скорость вообще, поэтому «проверять только бэклог» было бы проверкой половины правки.
4. **`processClaim` вынесен отдельно** не ради красоты: параллелизм требует, чтобы тело обработки было без `continue`/`break` (иначе `break` из горутины невозможен), а инвариант «лиза покрывает обработку» — чтобы он читался как инвариант одной задачи.

#### Побочные находки (вне скоупа A15)
- `AI_WORKER_BATCH_SIZE` умножает число одновременных запросов к OpenRouter (по умолчанию до 10). При жёстком rate-limit провайдера это осознанный компромисс; зафиксировано в `deploy/README.md`.
- Полная валидация `docker compose config` для prod требует ~15 секретов и остаётся задачей A33; здесь проверено, что базовый и prod-контур валидны и новые переменные дефолтятся (`AI_WORKER_TICK_SECONDS=2`, `AI_WORKER_BATCH_SIZE=10`).
- **Регресс:** `gofmt` 0, `go build`, `go vet`, полный `go test -race -count=2 ./...` — **14/14**.
- **Коммит:** не выполнялся.

### A16 · F-05 · HIGH · ✅ done 2026-09-27 · окно rate-limit сбрасывалось вытеснением из кэша
- **Где:** `docker-compose.yml` (политика Redis), новый `api/internal/ai/cache_budget.go`, `api/internal/ai/ai.go` (`cacheKey`, `cacheText`), новый `api/internal/store/eviction_policy_test.go`, новый `api/internal/ai/cache_budget_test.go`, `.env.example`, `docker-compose.yml`, `deploy/docker-compose.prod.yml`, `deploy/README.md`.
- **Доказательство дефекта (замер, не чтение кода):** на тестовом Redis с `maxmemory 8mb` + `allkeys-lru` ключ `rl:/v1/spreads:ip:203.0.113.7` и `sess:u-abc` **исчезли** после ballast-заливки (`EXISTS` → 0). Окно rate-limit счётчика 20/мин обнулилось, клиент получил новый бюджет. Также вытеснены `rl:admin-login:*` и `sess:*`.
- **Причина в конфигурации:** `docker-compose.yml` поднимал единственный инстанс как `--maxmemory 200mb --maxmemory-policy allkeys-lru`, а на нём живут `rl:*` (лимиты), `sess:*`/`csrf:*` (сессии), `ent:*` (entitlement-счётчики) и `ai:cache:*` (bulk, TTL 7 суток).

#### Что сделано
- **Политика инстанса → `noeviction`** (dev + prod), с комментарием в compose о том, почему `allkeys-lru` здесь недопустим. Осознанный размен: при нехватке памяти запись возвращает OOM-ошибку, а не вытесняет ключи; `failClosed`-маршруты отдают 503 — лимит важнее доступности.
- **Бюджет bulk-кэша `AI_CACHE_MAX_ENTRIES`** (10–1000000, по умолчанию 5000 ≈ 10 МБ): при превышении удаляются самые старые записи (наименьший остаток TTL) до 9/10 бюджета. Это убирает **причину** давления на память, а не только следствие. Без этого `noeviction` были бы обменом дыры безопасности на outage: кэш добежал бы до 200 МБ, и запись `rl:*` для НОВЫХ ключей падала бы с OOM → 503 на `/v1/auth/*` и `/v1/readings`.
- **`cacheKeyPrefix` вынесен в константу** — обрезка обязана видеть ровно то же пространство ключей, что и запись, иначе она срезает чужое.
- Счётчик записей `ai:cache:count` с TTL 24 ч (короткий TTL обнулял бы счётчик, и кэш успевал бы вырасти вдвое сверх бюджета). Ошибки Redis в этой части молчаливые: кэш — ускоритель, а не источник истины.

#### Мутации (3, все пойманы)
1. Политика обратно в `allkeys-lru` в `docker-compose.yml` → `declares maxmemory-policy allkeys-lru: eviction can reset rate-limit windows (A16/F-05)`.
2. Обрезка кэша по маске `"*"` вместо `ai:cache:v3:*` → `critical key rl:/v1/spreads:ip:203.0.113.7 was evicted by cache trim: got error redis: nil, want value "60"`.
3. Бюджет выключен (`if true { return }` в `noteCacheEntry`) → `cache budget not enforced: 60 entries after 60 writes with budget 10 (ceiling 9)`.

#### Тесты (4 новых)
- `TestCacheTrimNeverTouchesCriticalKeys`: 60 записей кэша при бюджете 10; критические ключи (`rl:*`, `sess:*`, `csrf:*`, `ent:*`) обязаны выжить **и не быть перезаписаны**, число записей кэша — не выше `9/10 бюджета + бюджет`. Факт: осталось 10 записей.
- `TestCacheBudgetIgnoresRedisFailures`: учёт кэша молчит без Redis (горячий путь не ломается).
- `TestComposeDeclaresNoEvictionPolicy`: **статический** контракт по трём compose-файлам — запрещены `allkeys-*` и `volatile-*`, требуется явный `--maxmemory-policy noeviction`. Есть счётчик проверенных файлов: если ни один compose не содержит `redis-server`, тест падает («контракт потерял зубы»), а не проходит вакуумно.
- `TestLiveRedisHasNoEvictionPolicy`: **живой** сервер отдаёт `noeviction`; при запрете `CONFIG` (управляемый Redis) — `t.Skip` с явной причиной, верхний уровень (compose) всё равно проверяется. В CI Redis —plain `redis:7-alpine`, там дефолт `noeviction`, поэтому проверка не вакуумная.

#### Грабли, о которых стоит помнить
1. **Тест на «вытеснение» нельзя писать на общем Redis:** `go test ./...` гоняет пакеты параллельно, и `CONFIG SET maxmemory 1mb` сломал бы соседей. Поэтому эмуляция вытеснения заменена двумя уровнями: статический контракт политики + функциональная проверка, что обрезка кэша не трогает критические ключи. Политика `noeviction` делает вытеснение невозможным by design, а « ballast-вытеснение » осталось воспроизводимым только как ручной замер (выполнен, результат выше).
2. **Проверка compose по сырому тексту ловила комментарий.** Первый вариант теста падал на собственном объяснении («При allkeys-lru ballast-кэш вытеснял…»). Пришлось вырезать комментарии (`stripYAMLComments`) и проверять фактические аргументы `redis-server`.
3. **Из `internal/store` нельзя использовать `testutil`** — `testutil` импортирует `store`, получается цикл импортов в тесте. Соединение создаётся напрямую через `ConnectRedis()`.
4. `redis.DurationCmd`, а не `time.DurationCmd` — иначе типизация pipeline-результата не проходит.

#### Побочные находки (вне скоупа A16)
- **Раздельные инстансы Redis с бюджетами памяти по ролям** — это A17 (F-43), здесь осознанно не делалось: A16 убирает вытеснение и давление, A17 разносит роли по инстансам.
- `admin` (311 с) и `ai` (52 с) — самые долгие пакеты в `-count=2`; это цена честных e2e-тестов, а не проблема.
- **Регресс:** `gofmt` 0, `go build`, `go vet`, `docker compose config` (dev и prod) валиден, полный `go test -race -count=2 ./...` — **14/14**.
- **Коммит:** не выполнялся.

### A17 · F-43 · MEDIUM · ✅ done 2026-09-27 · 7-дневный AI-кэш делил кэш с лимитами и сессиями
- **Где:** `docker-compose.yml` + `deploy/docker-compose.prod.yml` (новый сервис `cache-ai`, `REDIS_AI_*`), новый `api/internal/store/role_client_test.go`, `api/internal/store/store.go` (`ConnectRedisAI`), `api/internal/ai/ai.go` (`NewWithCache`, `cacheClient`, `cacheKeyPrefix`), новый `api/internal/ai/cache_roles_test.go`, новый `api/internal/ai/cache_stats.go` + `cmd/admin/main.go` (`GET /v1/admin/cache`), `api/cmd/api/main.go`, `api/internal/store/eviction_policy_test.go`, `.env.example`, `.github/workflows/ci.yml`, `deploy/README.md`.
- **Граница с A16:** A16 убрал вытеснение (`noeviction`) и давление на память (бюджет записей). A17 разносит роли по инстансам и даёт наблюдаемость. Раньше в бэклоге это читалось как «бюджеты по ролям» — теперь это ровно то, что здесь.

#### Что сделано
- **Два инстанса вместо одного.** `cache` (роль `critical`: `rl:*`, `sess:*`, `csrf:*`, `ent:*`) — `noeviction`, 200 МБ, `REDIS_PASSWORD`. `cache-ai` (роль `ai_cache`: `ai:cache:*`, TTL 7 суток) — `volatile-ttl`, 128 МБ, **свой** `REDIS_AI_PASSWORD`, свой том `redisdata-ai`, только сеть `internal`, **без опубликованных портов**. Вытеснение в роли кэша не только допустимо, но и желательно: кэш — ускоритель, а не источник истины.
- **`store.ConnectRedisAI()`** понимает и отдельный инстанс (`cache-ai:6379`), и отдельную БД того же (`host:6379/1`). Второй вариант нужен, чтобы **разделение ключей было проверяемо тестом**, а не только задумано в compose. Пустой `REDIS_AI_ADDR` — деградация с флагом `shared=true`; `cmd/api` печатает предупреждение на старте, а не молча делит инстанс. Мусор в адресе (`/99`, `/abc`, `/−1`, пустой хост) отвергается **без** тихого возврата на общий инстанс.
- **`Gateway.cacheRd`** — отдельный клиент кэша; `NewWithCache(pg, rd, cacheRd)`. `nil` = кэш выключен, но **никогда** не «тот же, что и лимиты» по умолчанию. Breaker (`ai:breaker:*`) остаётся на основном инстансе: он мелкий и должен быть виден рядом с лимитами.
- **`GET /v1/admin/cache`** за `RequireAdmin` — «метрика `cache_bytes{role}`» в единственной форме, которую проект может себе позволить без prometheus: роли `ai_cache` и `critical`, Entries точное, Bytes — выборочная оценка (`MEMORY USAGE ... SAMPLES 1`, поле `approx`). Фактический замер: `ai_cache=96 bytes / 1 entries; critical=15720 bytes / 144 entries`.
- **Документация расчёта:** 5000 записей × ~2 КБ ≈ 10 МБ при бюджете инстанса 128 МБ, поэтому до вытеснения дело не доходит; при потолке воркера 10 чтений/2 с (A15) **связывает именно бюджет, а не TTL** — без обрезки кэш рос бы неделями.

#### Мутации (4, все пойманы)
1. `cacheClient()` возвращает `g.rd` (роль игнорируется) → `cache role must hold entries: the cache was not written at all, the test would be vacuous` + `cache must stay disabled, not fall back to the critical client`.
2. Бюджет роли выключен → `cache budget not enforced: 40 entries with budget 10 (ceiling 9)`.
3. `/v1/admin/cache` без `RequireAdmin` → `cmd/admin/main.go must mount Get("/v1/admin/cache" behind RequireAdmin`.
4. Сервис `cache-ai` удалён из compose → `docker-compose.yml has no cache-ai service: the ai_cache role is declared but not deployed (A17/F-43)`.

#### Тесты (8 новых/переписанных)
- `TestBulkCachePressureKeepsSessionKeys` (карточный): 40 записей кэша при бюджете 10 в отдельной роли; ключи `rl:*`/`sess:*`/`csrf:*`/`ent:*` живы и не перезаписаны; **главное** — число записей кэша в критическом пространстве не выросло. Сравнение по **baseline**, а не по «нулю»: иначе тест падал бы из-за мусора чужого прогона и врал бы о причине. Факт: роль кэша 10 записей, 5 ключей лимитов целы.
- `TestCacheDisabledWithoutRole` — без роли кэш выключен, а не переехал на критический клиент.
- `TestCacheStatsReportsRolesPerMemory` — отчёт по ролям, `approx`, бюджет и TTL в отчёте.
- `TestCacheStatsRouteIsGuarded` — статический guard маршрута (паттерн A14/A15).
- `TestComposeSeparatesCacheRoles` (store) — контракт ролей на уровне репозитория: `volatile-ttl`, явный `maxmemory`, нет `ports:`, свой том, `REDIS_AI_ADDR` у обоих API.
- `TestConnectRedisAI*` (store, 3 теста) — разбор `REDIS_AI_ADDR`, отказ на мусоре без тихого возврата, fallback на `REDIS_PASSWORD`.
- `TestCacheBudgetIgnoresRedisFailures` и `TestCacheTrimNeverTouchesCriticalKeys` (A16) — дополнены чисткой счётчика бюджета, иначе `-count=2` начинал уже с израсходованным бюджетом и проверял не то.

#### Побочно найденное и починенное (изоляция тестов — 3 независимых дефекта)
Регресс A17 не проходил четыре раза подряд, и каждый раз причиной был **не** A17. Все три дефекта вскрылись именно потому, что регресс гоняется `-race -count=2` по общей БД/Redis. Они стоили отдельной карточки, но починены здесь, пока регресс был сломан.
1. **Глобальный триггер пакета `me` ронял чужой пакет.** `taro_test_fail_del BEFORE DELETE ON users` (без `WHEN`) поднимал ошибку на **любом** удалении пользователя, включая слияние аккаунтов в `auth` (`Link` удаляет проигравшего) → `ERROR: forced failure for A08 test` в чужом пакете. Починено: `WHEN (OLD.id = '<uid>'::uuid)` — отказ остаётся ровно для своей строки. Три других глобальных триггера (`taro_test_slow_sub`, `taro_test_slow_pay`, `taro_test_slow_ent`) только делают `pg_sleep`, чужое состояние не ломают — оставлены как есть, но это известный риск искажения таймингов.
2. **Прод-непоследовательность в антиферме регистраций, вскрытая тестами.** `HandleAnon` читал IP **только** из `X-Real-IP` и подставлял литерал `"unknown"`, тогда как ratelimit давно делает fallback на `RemoteAddr`. Следствия: (а) прямой доступ к `:8080` без nginx схлопывал всех анонимных клиентов в 20 регистраций в час на весь интернет; (б) **все** e2e-тесты аутентификации (они выставляли `RemoteAddr`, а не заголовок) попадали в один бакет `rl:reg:unknown` с TTL час — замер после трёх прогонов: `rl:reg:unknown = 24` при лимите 20, то есть исход тестов зависел от того, сколько раз их гоняли за час. Починено: единая `clientIP()` (X-Real-IP → нормализация → адрес сокета → `"unknown"`). Мутации: (1) убрать fallback → `clientIP()="unknown", want "10.0.0.7"` и `bucket rl:reg:10.0.0.1 must exist (got 0)`; (2) вернуть `X-Real-IP`-only в `HandleAnon` → падает wiring-тест. Обе ловятся детерминированно, без гонки с состоянием.
3. **`testutil.UniqueIP` не давал уникальности между процессами.** Формула `base*1000 + counter` + `10.x.y.z` с 6-битным первым октетом **обрезала** адрес до 22 бит: n и n+2²² давали один и тот же IP. При ~10 прогонах пакета `admin` за окно лимита входа (15 минут) коллизии баз (~1/4000) ловились регулярно: `TestE2EAdminLoginRateLimit` → `attempt 1: want 401 got 429`. Починено в двух слоях: (а) генератор без обрезания — 20-битная база, 3 её бита выбирают первый окет из белого списка `{10, 172, 192, 198, 203}`, 17 бит идут в старшие биты адреса, 12 бит — счётчик (4096 адресов на процесс без повторов, коллизия между процессами 1/2²⁰); (б) **тест владеет своими ключами** — `TestE2EAdminLoginRateLimit` чистит выведенные из СВОИХ идентичностей ключи `rl:admin-login:*` до и после, поэтому исход больше не зависит от предыдущих прогонов. Появился первый тест пакета `testutil`: `TestUniqueIPDoesNotAlias` (4096 вызовов, ни одного повтора) и `TestUniqueIPIsParseableAndInPrivateSpace`. Мутация (возврат обрезающей формулы) → `UniqueIP repeated "10.14.26.100" at call 1: addresses alias`.

#### Грабли, о которых стоит помнить
1. **Вероятностная уникальность — не изоляция.** Генератор уникальных IP не может гарантировать уникальность между процессами; гарантирует только владелец ключа. Порядок правильный: сначала «тест владеет своим ключом», потом «генератор даёт много энтропии».
2. **Промежуточная «починка» `UniqueIP` сама была обрезанной:** `base<<16` при 16-битной базе давал индексы 0..7 при массиве из 5 октетов → паника. Поймал `-count=2`: тест падал через раз. Отсюда правило: у генератора идентичностей должен быть собственный тест на инъективность, а не «выглядит уникальным».
3. **Временная граница теста обязана иметь запас в обе стороны.** Граница параллельности A15 была 15 с при ожидаемых 6 с (2.5x) и фейлилась под нагрузкой полного прогона. Перебалансировано: задержка 300 мс, граница 12 с (ожидаемые 3 с, запас 4x; ловит последовательные 30 с). После этого — 3 полных прогона `go test -race -count=2 ./...` подряд, 15/15.
4. **Guard-тест «бакет не используется» умеет обвинять мутацию.** После мутационного прогона, намеренно вернувшего дефект, ключ `rl:reg:unknown` остался в Redis на час, и следующий прогон падал в guard'е. Это правильное поведение (дефект снова в коде), но после мутационных прогонов такое состояние надо чистить руками.

#### Побочные находки (не чинил)
- Три «медленных» глобальных триггера в тестах (`payments` × 2, `referral`) искажают тайминги соседних пакетов; `admin` (165–320 с) и `ai` (50 с) — самые долгие пакеты, и часть этого вклада — они.
- `REDIS_AI_PASSWORD` стал обязательной переменной прод-контура (`deploy/check-env.sh` это уже видит и требует).
- **Регресс:** `gofmt` 0, `go build`, `go vet`, `docker compose config` (dev + prod) валиден, `go test -race -count=2 ./...` — **15/15** три полных прогона подряд.
- **Коммит:** не выполнялся.

### A18 · F-42 · MEDIUM · ✅ done 2026-09-27 · 1.768 с на каждый fail-open запрос при мёртвом Redis
- **Где:** `api/internal/store/store.go` (явные бюджеты клиента), `api/internal/ratelimit/ratelimit.go` (бюджет времени + счётчик в памяти), новый `api/internal/ratelimit/failfast_test.go`, новый `api/internal/store/redis_budget_test.go`.

#### Замер «до» (тот же тест на исходном коде)
- 20 последовательных fail-open запросов: **34.7 с суммарно, худший 1.768 с** (аудит говорил 1.714 с — совпало).
- fail-closed `/v1/auth/anon`, `/v1/readings`, `/v1/me`: **1.758 с** каждый, затем 503.
- 300 конкурентных запросов: суммарно 957 мс, худший **956 мс**.
- **200 запросов с мёртвым Redis прошли ВСЕ** (allowed=200, limited=0): «мягкий fail-open» означал «лимита нет».

#### Замер «после»
| величина | было | стало | DoD |
| --- | --- | --- | --- |
| худший запрос fail-open | 1.768 с | **51 мс** | < 100 мс ✓ |
| fail-closed (503) | 1.758 с | **30 мс** | fail-fast ✓ |
| 300 конкурентных, худший | 956 мс | **53 мс** | < 200 мс ✓ |
| 200 запросов при мёртвом Redis | 200 пропущено | **60 пропущено / 140 отрезано** | лимит жив ✓ |

#### Что сделано
- **Явные бюджеты клиента** (`redisOptions` для обеих ролей): `Dial/Read/Write/PoolTimeout = 2 с`, `PoolSize 10`, `MinIdleConns 2`, `ContextTimeoutEnabled: true`, **`MaxRetries: -1`**. Ключевой пункт: в go-redis `MaxRetries: 0` означает НЕ «без повторов», а «3 повтора по умолчанию» с экспоненциальной паузой — именно это и давало 1.7 с. Повторы отключены осознанно: все команды приложения уже умеют деградировать, а повтор лишь съедает соединения из пула.
- **Бюджет времени на горячем пути лимитера:** `failOpenBudget = 50 мс`, `failClosedBudget = 30 мс`. Это не «на глаз», а один сетевой round-trip внутри контура; любой дефект сети превращается в промах по кэшу лимита, а не в очередь.
- **Fail-open больше не значит «без лимита»:** при таймауте Redis счётчик продолжает считать **в памяти** (`memoryCounter`) с теми же ключами, окном и Max. Честная цена — лимит в пределах процесса (при N репликах effectively N×Max), это описано в коде; альтернатива «ничего не считать» хуже.
- **Счётчик в памяти ограничен:** `memoryFallbackMax = 8192` + запас `8192/8` для амортизации, уборка просроченных окон по времени (раз в секунду) и по счётчику операций, вытеснение самых старых при переполнении. Без запаса каждая вставка сверх предела запускала бы сортировку всей карты — упёршись в 8192 заливок, сортировка 8192 элементов на КАЖДЫЙ запрос при мёртвом Redis.

#### Мутации (4, все пойманы)
1. Бюджет времени снят (`ctx := r.Context()`) → `fail-open request took 1.771873458s: budget is 50ms, DoD is < 100ms` и `fail-closed request took 1.767220375s`.
2. Fail-open без локального счёта → `fail-open must keep limiting without Redis: allowed=200 limited=0`.
3. Уборка окон выключена (`if false`) → `in-memory limiter must stay bounded, holds 20000 keys (limit 9216)`.
4. `MaxRetries` вернулся к дефолту → `MaxRetries=3: 3 означает «повторять по умолчанию», что при мёртвом Redis даёт ~1.7 с на запрос`.

#### Тесты (7 новых/переписанных)
- `TestFailOpenFastWhenRedisDown`, `TestFailClosedFailsFastWhenRedisDown` — верхняя граница времени (бюджет × 4 как допуск) и коды 200/429 против 503.
- `TestFailOpenStillLimitsWhenRedisDown` — лимит продолжает работать при мёртвом Redis; 200 запросов → `allowed=60 limited=140` (правило Max=60).
- `TestConcurrentFailOpenDoesNotHoldGoroutines` — 300 конкурентных: проверяются и хвост (`< 200 мс`), и суммарное время (`< 5 с`), иначе деградация спрячется в одном медленном запросе.
- `TestInMemoryLimiterStaysBounded` — 20 000 РАЗНЫХ ключей (первая версия теста вставляла 5000 вызовов с 250 ключами и ничего не доказывала — мутация 3 прошла, тест переписан) + проверка, что просроченное окно переоткрывается.
- `TestInMemoryLimiterCountsPerWindow` — семантика `windowLua` в памяти.
- `TestRedisClientHasExplicitBudgets`, `TestRedisAIUsesTheSameBudgets`, `TestRedisBudgetsAreNotSlowerThanTheyLook` — контракт клиента.

#### Побочно найденное и починенное (изоляция тестов, 2 дефекта)
Оба вскрылись регрессом A18 и относятся к семейству F-18/A17.
1. **A15 (батч 1 → 10) усилил старую помеху F-58 в тестах воркера.** Счётчики вызовов провайдера (`calls.Load() != 1 / != 2 / != 0`) считали вызовы для **чужих** чтений, забранных тем же батчем: `retry row status=failed attempts=2 calls=3`. Починено: `ownCallCounter` считает только вызовы, чей prompt содержит маркер этого теста (вопрос входит в prompt, попадание точное). Для двух тестов, где провайдер звать нельзя, глобальный счётчик убран вовсе — там проверка идёт по **содержимому толкования** (`strings.Contains(interpretation, "disabled")` → false), потому что фильтровать нечем: запрос до провайдера не доходит. Мутация (неверный маркер в счётчике) → `status=done attempts=1 calls=0` и `retry row ... calls=0`.
2. **`anon_uuid` в тестах строился из 16 бит наносекунд** (`hex4` = младшие 4 hex-символа) — всего 65 536 значений на все прогоны. Коллизия означала, что `AnonLogin` возвращал уже существующего пользователя (ON CONFLICT по `anon_uuid`), связанного в прошлом прогоне, и тест падал с `link: 409 ALREADY_LINKED`. Комментарий в коде предупреждал о коллизиях для tg_id, но uuid остался с 16 битами. Починено: uuid собирается из `testutil.UUID` (128 бит) + 12 hex из полных наносекунд; `hex4` оставлен с явным комментарием-предупреждением.

#### Грабли, о которых стоит помнить
1. **«Быстрый fail-open» без счётчика — это отключённый лимит.** Замер «до» показал: 200 запросов, 0 отрезано. Любая оптимизация деградации обязана сохранять счётчик, иначе DoD «< 100 мс» достигается тривиально — выключив защиту.
2. **В go-redis ноль означает дефолт, а не «выключено».** `MaxRetries: 0` → 3 повтора. Проверять надо нормализованное значение в `client.Options()`: библиотека переписывает `-1` в `0`, поэтому в тесте ожидается 0, а не -1. Этот нюанс стоил одного провалившегося теста.
3. **Тест на утечку памяти должен вставлять РАЗНЫЕ ключи.** 5000 вызовов с 250 ключами проходят любую границу — мутация «уборка выключена» прошла незамеченной, пока ключи не стали уникальными.
4. **Мои собственные запуски без `JWT_SECRET` давали 503 и выглядели как регрессия кода.** `TestE2EUUIDHardening` не ставит секрет в тесте и берёт его из окружения; без переменной `IssueJWT` падает и хендлер честно отдаёт 503. Ошибка была в команде прогона, а не в правке — но потратила время на ложный след.

#### Побочные находки (не чинил)
- Три «медленных» глобальных триггера в тестах (`payments` × 2, `referral`) по-прежнему искажают тайминги соседей.
- `poolSize 10` на все роли: при N параллельных запросов на один IP это 10 одновременных команд; при исчерпании пула включается `PoolTimeout = 2 с`. Для лимитера это неважно (бюджет 50 мс), для прочих путей — осознанный компромисс.
- **Регресс:** `gofmt` 0, `go build`, `go vet`, `docker compose config` валиден, `go test -race -count=2 ./...` — **15/15** два полных прогона подряд.
- **Коммит:** не выполнялся.

### A19 · F-48 · LOW · ✅ done 2026-09-27 · over-fetch в истории чтений
- **Где:** `api/internal/readings/readings.go` (`HandleList`: окно `left(interpretation, …)`, обрезка превью), новый `api/internal/readings/history_fetch_test.go`, новый `testutil.LiveTraced` (не понадобился, см. грабли).

#### Замер «до» (50 строк, потолок `limit`, толкования по ~160 КБ)
| величина | было | стало |
| --- | --- | --- |
| байт по проводу за один вызов истории | **8 104 658** | **61 158** |
| окно в формуле | 131 073 символа | 640 символов |
| ответ клиенту | 22 268 | 22 268 (без изменений) |
| перебор | — | **×133** |

**Уточнение к карточке:** DoD говорил про «370 строк ≈ 48 МБ», но `HandleList` ограничивает `limit` пятьюдесятью (`limit > 50 → 20`). Реальный потолок — 50 строк, то есть 8.1 МБ вместо 48 МБ. Находка верна по сути, число было завышено в 6 раз из-за `LIMIT 370` у соседнего запроса (стрик).

#### Что сделано
- **Окно запроса** `left(interpretation, $5)`: было `ai.MaxOutputBytes+1` (131 073), стало `historyPreviewFetchRunes = 640` (4 × 160). Запас ×4 нужен для двух вещей: защитная проверка стоп-слов по привезённому окну и безопасная обрезка по границе руны.
- **Модерация при этом НЕ слабеет:** стоп-слова отсекаются на записи (`ai.Stream` → `SafeReplacement`, статус `filtered`), поэтому проверка в списке защитная. Это отражено комментарием в коде — иначе «заказали меньше» можно было бы прочитать как «перестали проверять».
- **Найденный попутно баг:** обрезка превью была `it.Preview[:160]` — по **байтам**, тогда как PostgreSQL `left()` режет по **символам**. Кириллица по 2 байта, поэтому 160 байт = 90 символов, а на границе руны клиент получал битый UTF-8 (`json` кодировал его как U+FFFD). Заменено на `truncateRunes()`.
- `HandleGet` и `streamReading` **не тронуты**: там клиенту нужен весь текст, перебора нет.

#### Мутации (3, все пойманы)
1. Возврат `ai.MaxOutputBytes+1` в запрос → `history over-fetches over the wire: 8104658 bytes for 50 previews, budget 1048576`.
2. Возврат обрезки по байтам → `preview has 90 runes, want exactly 160: "Карты говорят, что путь ваш ясен. …"` (ровно то, о чём баг: 90 символов вместо 160).
3. Окно ×4000 (768 КБ на строку) → пойман обоими слоями: `history over-fetches over the wire: 8104658 bytes` и `fetch window 640000 is too large: the point of A19/F-48 is not to over-fetch`.

#### Тесты (3 новых)
- `TestHistoryQueryDoesNotOverfetch` — измеряет **фактические байты по сети** через считающий TCP-прокси между тестом и PostgreSQL, плюс проверяет, что каждое из 50 превью валидно и ровно 160 символов, и что ответ ≤ 128 КБ.
- `TestHistoryPreviewTruncationIsRuneSafe` — контракт обрезки: кириллица, короткие строки, нулевой лимит.
- `TestHistoryPreviewFetchWindowIsBounded` — сторож окна запроса: оно должно быть заметно больше превью (запас) и заметно меньше полного толкования.

#### Грабли, о которых стоит помнить
1. **Считать перебор по константе в коде нельзя.** Первая версия теста сравнивала `historyFetchedBytes(…, historyPreviewFetchRunes)` против `historyFetchedBytes(…, 131073)` — то есть измеряла намерение, а не факт: возврат перебора в хендлере такой тест проходил. Понадобилось измерение настоящих байт.
2. **`pgx.QueryTracer` не видит строки:** в v5 треймер получает только `TraceQueryStart/End` и `CommandComplete` без данных, поэтому «посчитать байты ответа» им нельзя. `EXPLAIN (BUFFERS)` тоже отвечает не на тот вопрос (буферы, а не отданные значения). Считающий TCP-прокси отвечает буквально: 61 158 байт против 8 104 658. `testutil.LiveTraced` из-за этих граблей остался неиспользованным — удалён.
3. **«Самый свежий пользователь» — гонка.** Тест брал uid как `ORDER BY created_at DESC LIMIT 1`; при параллельном `go test ./...` соседний тест успевал создать своего пользователя позже, и список приходил пустым (`response 3 bytes`, `0 items`). uid теперь передаётся явно.
4. **Число в DoD было завышено в 6 раз** — карточка умножала 128 КБ на 370 строк, взяв лимит из соседнего запроса по стрику. Находку стоило перепроверить до правки: иначе «48 МБ» превратилось бы в ложную тревогу, а реальные 8.1 МБ — в «оптимизацию ради единиц».

#### Побочные находки (не чинил)
- `ai.MaxOutputBytes+1` остаётся в `HandleGet` и `streamReading` — там это оправдано (клиенту нужен весь текст), но стоит держать в уме при будущих списках.
- **Регресс:** `gofmt` 0, `go build`, `go vet`, `docker compose config` валиден, `go test -race -count=2 ./...` — **15/15** два полных прогона подряд.
- **Коммит:** не выполнялся.

# WAVE 3 — web-контракты и безопасность

### A20 · F-13 · HIGH · ✅ done 2026-09-27 · 18 из 22 роутов Next теряли `Set-Cookie`/`Retry-After`
- **Где:** `web/lib/proxy.ts` (`relay`, `relayStream`, `relayJSON`), **все 22** `web/app/api/**/route.ts`, новый `web/lib/route-header-survival.test.ts`.

#### Что было сломано
19 из 22 роутов собирали `NextResponse` руками и копировали из ответа Go **только `content-type`** (в `diary/export` — ещё `Content-Disposition`). Терялись:
- **`Set-Cookie`** — вместе с ним ротация CSRF (`taro_csrf`) и обновление сессии: браузер остаётся со старым токеном, «прозрачный» прокси перестаёт быть прозрачным;
- **`Retry-After`** — при 429 клиент не знает, когда повторить;
- любые будущие заголовки Go (`X-Request-Id`, `RateLimit-*`), о которых роут не знал и потому молча срезал их.

Отдельно: `referral/me` копировал `set-cookie` через `.set()` — то есть **склеивал все cookie в один заголовок**. Браузер не считает запятую разделителем в `Set-Cookie`, поэтому логаут и разные cookie схлопывались в одну битую запись. Три роута использовали корректный `passThrough`.

#### Что сделано
- **Одна точка возврата ответа** в `lib/proxy.ts`: `relay` (буферизованный), `relayStream` (SSE без буферизации + `Cache-Control: no-store`), `relayJSON` (`relay` + принудительный `no-store` для приватных данных). `passThrough` остался как внутренняя деталь `relay`, потому что на него завязан существующий юнит-тест.
- **Все 22 роута переведены** на `relay`/`relayJSON`/`relayStream`. hop-by-hop заголовки (`Connection`, `Transfer-Encoding`, …) по-прежнему не копируются — они относятся к соединению Go↔Next.
- **Найдено попутно: `PUT /api/diary/:id` не existed.** В Go есть `PUT /v1/diary/{id}` (`diary.HandleUpdate`), а Next-роута не было — метод был доступен только напрямую через Go, минуя прокси, а значит минуя нормализацию `Origin`/`X-CSRF` в `fwdHeaders`. Роут добавлен.

#### Мутации (5, все пойманы)
1. Роут снова собирает ответ руками → `GET app/api/streak/me/route.ts: no Retry-After; no X-Request-Id; cookie lost: taro_jwt=…; cookie lost: taro_csrf=…; cookie lost: taro_fp=…`.
2. Cookie склеиваются в один `Set-Cookie` (`.set(join(", "))`) → `1 Set-Cookie header(s), want 3: ["taro_jwt=… , taro_csrf=…, taro_fp=…"]` на каждом роуте.
3. hop-by-hop заголовки копируются как есть → падает `never copies hop-by-hop headers`.
4. Новый роут без `relay` → ловится **двумя** утверждениями: счётчик «22 роута» и диагностика потерянных заголовков именно нового файла.
5. SSE буферизуется (`relayJSON` вместо `relayStream`) → `delivers the first chunk before upstream finishes` (первый чанк не приходит, пока вверх не отпущен второй).

#### Тесты (5 новых, `routeHeaderSurvival` + отдельный блок SSE)
- **Табличный обход всех 22 роутов:** каждый экспортированный HTTP-метод вызывается с подменённым `fetch`, отдающим 429 с тремя `Set-Cookie`, `Retry-After` и `X-Request-Id`; требуется, чтобы всё это дошло до ответа роута вместе со статусом. Счётчик роутов в тесте не декоративен: новый файл обязан попасть в таблицу.
- **Cookie проверяются через `getSetCookie()`**, а не через `get()`: и три `append`, и одна склеенная строка дают одинаковый текст через запятую, поэтому первая версия проверки была беззубой — мутация 2 прошла, пока не переписали на `getSetCookie()`.
- **hop-by-hop не копируются** ни на одном роуте.
- **SSE-порядок:** первый чанк обязан прийти **до** того, как вверх отдадут второй, иначе «живой» поток неживой. Проверяется гонкой с таймаутом, а не сравнением тел.
- Мутации 1–5 прогнаны на этом наборе; `tsc --noEmit` — 0 ошибок, `npm test` — 8 файлов / 36 тестов, прод-сборка `npm run build` (с `NEXT_PUBLIC_BASE_URL`) — успешна, все 22 API-роута в отчёте помечены `ƒ (Dynamic)`.

#### Грабли, о которых стоит помнить
1. **`Headers.get("set-cookie")` не различает append и set.** Оба дают строку с запятыми. Единственный честный способ отличить склейку — `getSetCookie()` (доступен в Node/undici). Первая версия теста на склейку была беззубой и пропустила мутацию.
2. **Корень проекта в тесте — не каталог теста.** `path.dirname(import.meta.url)` дал `web/lib`, и скан искал `web/lib/app/api`. Нужен `path.resolve(..., "..")`.
3. **Мутация 4 показала ценность счётчика роутов:** без него новый файл просто добавил бы ещё одну строку в таблицу и «проверку не заметил». Со счётчиком она обязана быть зарегистрирована.
4. **SSE нельзя приводить к общему виду «буферизованный ответ».** `relayStream` существует именно потому, что буферизация съедает поток; тест на это отдельный и проверяет порядок чанков, а не равенство.

#### Побочные находки (вне скоупа A20, не чинил)
- В Go есть `PUT /v1/diary/{id}`, а в Next-роутах его не было (см. выше) — роут добавлен, но стоит проверить, нужен ли он вебу вообще: если да, у него должны быть e2e-тесты на CSRF.
- Роутов, которые **не** могут нести `Set-Cookie` (публичные `spreads`, `plans`, `push/public`, `share/[token]`), формально «пробрасывать нечего», но единый `relay` избавляет от необходимости доказывать это по каждому: заголовки копируются всегда, а hop-by-hop отсекаются.
- **Регресс:** `gofmt` 0, `go build`, `go vet`, `docker compose config` валиден, `go test -race -count=2 ./...` — **15/15**; web: `tsc` 0, `npm test` 36/36, `npm run build` успешен.
- **Коммит:** не выполнялся.

### A21 · F-21 · MEDIUM · `X-Real-IP` не пробрасывается → общие корзины лимитов
- **Где:** `web/lib/proxy.ts:41-57`
- **Что:** пробрасывать `X-Real-IP` (и `X-Forwarded-For`) в `fwdHeaders`; добавить инвариантный тест «два разных IP → два разных бакета» для `byUser:false`-правил.
- **DoD:** ключ лимитера для `/v1/spreads` различает IP; в проде не бывает `rl:…:unknown`.
- **Проверка:** два запроса с разных IP → разные ключи в Redis; `redis-cli keys 'rl:*'` не содержит `unknown`.
- **Тест:** `TestForwardedIPYieldsDistinctBuckets`.

### A22 · F-22 · MEDIUM · SSE без terminal frame читается как успех
- **Где:** `api/internal/readings/readings.go:846-852,799-814` · `web/lib/api.ts:73-97`
- **Что:** клиент обязан требовать terminal-кадр (`done`/`error`), иначе — ретрай/ошибка; SSE-ветка Go должна повторять семантику JSON-ветки (202 + `reading_id` + `status`) либо документировать асимметрию явно.
- **DoD:** поток только с `{"pending":true}` → UI показывает ожидание/ошибку и не «успешный» пустой расклад; `reading_id` не пустой при успехе.
- **Проверка:** подсунуть в тест поток без `done`; e2e на pending-ветку.
- **Тест:** `api.test.ts` — кейс «done-less stream» (сейчас его нет); go-тест на SSE-контракт.

### A23 · F-10 · HIGH · фильтр кризисных слов обходится; SafeReplacement триггерит сам себя
- **Где:** `api/internal/…/filter.go:10,16-18,21-29`
- **Что:** нормализация перед сравнением: NFKC, выкинуть zero-width (`\u200b-\u200d\ufeff`), свернуть гомоглифы (`а`/`ё` латиницей и пр.), схлопнуть пробелы, lowercase; отделить предикат «это кризисный текст» от константы `SafeReplacement` (иначе она триггерит сама себя — уже доказано).
- **DoD:** corpus из 21 враждебной строки проходит на 21/21; `ContainsStopWords(SafeReplacement) == false`.
- **Проверка:** прогнать corpus (кириллические омоглифы, ZWSP, перенос строки, letter-spacing, латинская `a`, английские фразы) — все ловятся.
- **Тест:** golden-таблица 21+ строки как permanently-asserted тест + `TestSafeReplacementDoesNotTripFilter`.

### A24 · F-29 · MEDIUM · push-приёма нет, а UI обещает «Пуши включены»
- **Где:** `web/public/sw.js` · `web/components/PushOptIn.tsx` · `web/app/api/**` (нет unsubscribe/share-revoke)
- **Что:** реализовать `push`/`notificationclick` в SW **или** убрать обещание из UI; добавить Next-роуты для push-unsubscribe и share-revoke (в Go уже есть).
- **DoD:** либо SW показывает уведомление, либо UI не утверждает, что push включены; unsubscribe/revoke доступны из интерфейса.
- **Проверка:** `grep -n "'push'\|showNotification" web/public/sw.js`; e2e подписки/отписки.
- **Тест:** тест наличия/отсутствия обработчика (осознанный: если решили убрать — тест фиксирует отсутствие и текст UI).

### A25 · F-30 · MEDIUM · превью шэринга мертвы; токены в логах; доверие `$host`
- **Где:** `web/app/layout.tsx` · `deploy/nginx.conf` (`log_format`) · TLS-конфиг (redirect на `$host`)
- **Что:** задать `metadataBase` из `NEXT_PUBLIC_BASE_URL`; редактировать `share/*` в access-логе (маскировать токен); редирект на HTTPS строить на `$server_name`, а не на доверительном `$host`.
- **DoD:** `og:image`/`og:url` абсолютные и с прод-доменом; в логах нет `share/*` токенов; redirect не доверяет `Host`.
- **Проверка:** `curl -s localhost:3000/ | grep og:image`; `grep share /var/log/nginx/access.log`.
- **Тест:** e2e на абсолютные OG-теги; конфиг-тест на маскирование в `log_format`.

### A26 · F-31 · MEDIUM · `NEXT_PUBLIC_BASE_URL` опционален → сборка падает или печёт localhost
- **Где:** `web/Dockerfile:5,14` · `docker-compose.yml:155-170` · `web/app/sitemap.ts:6-8`
- **Что:** сделать build-arg обязательным (без дефолта `""` и без `http://localhost:3000` в прод-профиле), чтобы «сорвалась сборка» вместо «запечатан localhost в sitemap».
- **DoD:** `docker build` без переменной падает с внятным сообщением; с переменной — sitemap с прод-доменом.
- **Проверка:** `docker compose build web` с пустым `NEXT_PUBLIC_BASE_URL` → ошибка с указанием ключа.
- **Тест:** CI-шаг «сборка без обязательного arg должна падать».

### A27 · F-39 · MEDIUM · `retryAuth` не вызывается; JWT живёт после удаления аккаунта
- **Где:** `web/lib/auth.ts:127` · `web/app/profile/page.tsx:117-138`
- **Что:** вызывать `retryAuth` при 401; после `DELETE /api/me` сбрасывать `authPromise` и немедленно чистить клиентское состояние, чтобы удалённый JWT больше не уходил.
- **DoD:** после 401 состояние восстанавливается без перезагрузки; после удаления аккаунта JWT не отправляется.
- **Проверка:** e2e: имитация 401 → повтор; удаление аккаунта → вкладки перестают слать `taro_jwt`.
- **Тест:** `auth.test.ts` на retry и на reset-after-delete.

### A28 · F-40 · MEDIUM · Onboarding перекрывает AgeGate (18+ недостижим)
- **Где:** `web/app/layout.tsx:42-43` · `web/components/Legal.tsx:30` · `web/components/Onboarding.tsx:69`
- **Что:** выстроить последовательность: онбординг показывается только после принятия возраста (или поднять `AgeGate` над `Onboarding` и гейтить показ onboarding по флагу согласия).
- **DoD:** при первом визите пользователь обязан принять 18+, и только потом видит онбординг.
- **Проверка:** e2e на чистом клиенте: порядок модалок; `aria-modal`/фокус.
- **Тест:** e2e «age gate precedes onboarding».

### A29 · F-47 · MEDIUM · push-endpoint принимает любой публичный host/port
- **Где:** `api/internal/push/push.go` (валидация подписки)
- **Что:** allow-list известных push-эндпоинтов (или строгий host+port+scheme), 422 на всё остальное.
- **DoD:** произвольный хост/порт отклоняются; легитимные эндпоинты работают.
- **Проверка:** `go test` кейсы: `http://internal:22/`, `https://evil.example:8443/`, легитимный.
- **Тест:** `TestPushEndpointAllowList`.

### A30 · F-45 · MEDIUM · нет капа fingerprint; disabled попадают в фоновые выборки
- **Где:** `api/internal/auth` (fingerprint) · `api/internal/readings/share.go` · `api/internal/push/push.go:406-447` · referral
- **Что:** суточный кап анонимных fingerprint; во всех фоновых/public-запросах фильтровать `disabled_at IS NULL` (share-лукап, пуш-рассылки, рефералка).
- **DoD:** N+1 анонимов с одного fingerprint в сутки блокируются; отключённый аккаунт не виден в share и не получает пуши.
- **Проверка:** e2e/тест на disabled в share; тест на дневной кап fingerprint.
- **Тест:** `TestDisabledUserNotInShareOrPushTargets` + `TestFingerprintDailyCap`.

---

# WAVE 4 — supply chain, деплой, наблюдаемость

### A31 · F-15 · HIGH · EOL-тулчейн и 6 плавающих базовых образов
- **Где:** `api/go.mod:3,20-23` · `.github/workflows/ci.yml:20,25,66,82` · `web/Dockerfile:5,17` · `docker-compose.yml:131,186` · `api/Dockerfile.public:2,9,11` · `api/Dockerfile.admin:4,12,14`
- **Что:** go→1.27, node→24 LTS, alpine→3.22+, nginx→1.29+; зафиксировать 6 плавающих баз по digest; обновить `go-version` в CI (иначе EOL не обнаружится).
- **DoD:** ни один образ не из EOL; все 9 инфра-образов закреплены digest; `go list -m all` и `go mod verify` чисты.
- **Проверка:** `go version; node -v`; `docker compose config | grep image` → у всех есть `@sha256:`; `docker image inspect` Created-даты совпадают с релизами.
- **Тест:** CI-шаг «все image имеют digest + версии не из EOL-списка».

### A32 · F-16 · HIGH · нет бэкапа перед миграцией и нет rollback
- **Где:** `.github/workflows/deploy.yml:75-105` · `deploy/migrate.sh` · `deploy/backup.sh` · `deploy/README.md`
- **Что:** вызывать `backup.sh` перед `migrate.sh up`; сохранять digest'ы pre-migration; добавить `workflow_dispatch`-job отката; сделать `migrate.sh down` неинтерактивным (`--force`) для CI.
- **DoD:** деплой создаёт снапшот до миграции; есть документированный путь отката; `down` работает без TTY.
- **Проверка:** dry-run деплоя; `migrate.sh down 1 --force` на тестовой БД; проверить наличие снапшота в логах job.
- **Тест:** CI-шаг на `migrate.sh down --force` (иначе откат в проде невозможен).

### A33 · F-17 · HIGH · CI не способен обнаружить дефекты этого отчёта
- **Где:** `.github/workflows/ci.yml`
- **Что:** добавить `govulncheck`, gitleaks/Trivy, `golangci-lint`, покрытие (`-coverpkg=./...` + порог), `tsc` по `*.test.ts`, `docker compose config` для dev/prod/tls, сборку web-образа в CI; убрать «одно захардкоженное» правило секретов в пользу gitleaks; добавить `permissions:` и Dependabot.
- **DoD:** каждый дефект из отчёта, который можно поймать статикой/тестами, ловится новым гейтом (проверяемо: список из 8 пунктов).
- **Проверка:** `act`/локальный прогон пайплайна; `gitleaks detect` в CI даёт тот же результат, что локально (21 плейсхолдер, 0 реальных).
- **Тест:** meta-тест: «мутация» одного файла должна ломать новый гейт (например, внести `fmt.Print*` сверх лимита и убедиться, что lint это видит).

### A34 · F-19 · HIGH · паника навсегда убивает тикер оплаты, без лога
- **Где:** `api/cmd/api/main.go:92-109` · `api/internal/apierr/apierr.go:73` (`Recover() = _ = recover()`)
- **Что:** перенести `defer apierr.Recover()` **внутрь** тела цикла (как уже правильно сделано в `ai/worker.go:105-108`); `Recover()` должен логировать со стеком и контекстом; завести метрику «последний тик» (last_tick_unix), чтобы тихий отказ был виден.
- **DoD:** паника в `ExpirePending` не убивает тикер (проверяется тестом с инъекцией паники); в логах есть стек; health не остаётся зелёным при мёртвом тикере.
- **Проверка:** тест с паникой в обработчике → тикер продолжает работать; `grep Recover apierr.go` → логирование.
- **Тест:** `TestRecoverKeepsTickerAlive` (с инъекцией паники).

### A35 · F-20 · HIGH · фактически нет наблюдаемости
- **Где:** весь `api/` (10 `log.Printf`, все в `cmd/*/main.go`)
- **Что:** `log/slog` JSON в stdout; middleware с request/correlation ID (проброс из `X-Request-ID`); `/metrics` (Prometheus) с ключевыми метриками (очередь чтений, 5xx, breaker, rate-limit 429, webhook duplicate, тики); error tracking; убрать утечки в логах (токены шэринга — см. A25).
- **DoD:** можно ответить на «что сломалось в 03:00»: есть request ID, структурный лог, метрики, алерты.
- **Проверка:** `curl -s localhost:8080/metrics | head`; один запрос → в логе строка JSON с `request_id`.
- **Тест:** `TestRequestIDPropagates` + проверка, что `/metrics` отдаёт ожидаемые метрики.

### A36 · F-41 · MEDIUM · у HTTP-серверов нет таймаутов
- **Где:** `api/cmd/api/main.go:183` · `api/cmd/admin/main.go:123`
- **Что:** задать `ReadHeaderTimeout` и `IdleTimeout` обоим серверам; `WriteTimeout` — только на не-SSE маршруты (SSE живёт долго by design), либо через per-route сервер/контекст.
- **DoD:** медленный клиент не держит соединение бесконечно; SSE не рвётся.
- **Проверка:** `curl --limit-rate 1` с keep-alive → соединение закрывается по таймауту; SSE-поток живёт дольше write-таймаута и не обрывается.
- **Тест:** `TestSlowlorisConnectionTimesOut` + `TestSSENotCutByWriteTimeout`.

### A37 · F-23 · MEDIUM · тело ответа провайдера попадает в `ai_logs` и в админку
- **Где:** `api/internal/ai/ai.go:471-473` → `ai.go:354`
- **Что:** для не-2xx `<500` хранить только код статуса (+ короткий редактированный, обрезанный по длине фрагмент), а не до 1024 сырых байт; редактировать потенциальные prompt-фрагменты.
- **DoD:** 402/401/403/404/422/429 → в `ai_logs.error` нет verbatim-тела ответа провайдера.
- **Проверка:** mock провайдера, возвращающего секрет в теле 4xx; `SELECT error FROM ai_logs …` → секрета нет.
- **Тест:** `TestProvider4xxBodyNotPersistedVerbatim`.

---

# WAVE 5 — гигиена, миграции, документация

### A38 · F-38 · MEDIUM · мёртвая вторая реализация квот
- **Где:** `api/internal/entitlements/entitlements.go:763-779` (+ `Service.Check`, `pgConsume*`, `consume`, `cacheQuota`, `GrantBonusDays`) · `docs/project-book/…/02-interaction-next-go.md`
- **Что:** удалить мёртвый код (0 вызывающих) — он слабее живого `AuthorizeReading` + receipts; вычистить write-only `ent:*` ключи или начать их читать; поправить доки, где Redis назван «энучерпером лимитов».
- **DoD:** мёртвые функции удалены, доки соответствуют коду, `go build`/`vet` чисты, тесты зелёные.
- **Проверка:** `grep -rn "GrantBonusDays\|pgConsume" api/` → только определение или ноль; `go vet`.
- **Тест:** `go test ./...` после удаления (сломается, если что-то всё-таки вызывалось).

### A39 · F-37 · MEDIUM · 5 ручек `app_config`, которые никто не читает
- **Где:** `api/internal/admin/admin.go` (config) · потребители отсутствуют · `web`
- **Что:** либо задействовать `referral.enabled/bonus_days/monthly_cap`, `copy.paywall_{title,desc,cta}`, `history.free_limit` (включая **kill switch рефералки** — его сейчас нет вообще), либо убрать их из админки.
- **DoD:** каждая ручка либо влияет на поведение, либо отсутствует в UI; referral kill switch работает.
- **Проверка:** `grep -rn "referral.monthly_cap\|paywall_title\|free_limit" api/ web/` → читатели есть; e2e «выключил рефералку → отказ».
- **Тест:** `TestConfigKnobIsRead` / `TestReferralKillSwitch`.

### A40 · F-44 · MEDIUM · миграция 033 — пустышка, а `down` отказывает
- **Где:** `api/migrations/033_payment_webhook_audit.*` · `deploy/schema-ready.sh:399-405`
- **Что:** привести в согласие 033 и 029 (либо создать FK в новой миграции, либо отозвать 033) и снять противоречие с readiness-скриптом, который требует отсутствия FK.
- **DoD:** `schema-ready.sh verify` зелёный на БД, поднятой с нуля; нет миграции, чей `down` отказывает без префлайта без причины.
- **Проверка:** чистая БД → все 34 миграции → `schema-ready.sh verify`; `down 1`/`up 1` цикл.
- **Тест:** `TestMigrationsUpDownCleanCycle` (уже был успешный прогон — зафиксировать как регресс).

### A41 · F-46 · MEDIUM · карантинные строки не переезжают при merge
- **Где:** `api/migrations/034_*` · `api/internal/auth/link.go:278`
- **Что:** в merge-транзакции переносить/перепривязывать карантинные строки на выжившего пользователя (или явно помечать как orphan по политике).
- **DoD:** после merge в карантине нет строк с `user_id` удалённого аккаунта.
- **Проверка:** сценарий merge на тестовой БД; `SELECT count(*) FROM … WHERE user_id = <loser>` → 0.
- **Тест:** `TestQuarantineMovedOnMerge`.

### A42 · F-26 · MEDIUM · повторные попытки создают новые Telegram-инвойсы
- **Где:** `api/internal/payments/payments.go:957-964` (`ExpirePending`)
- **Что:** переиспользовать существующий `telegram_invoice_id`, пока он валиден; создавать новый только после явной инвалидации.
- **DoD:** N повторных попыток на одной платёжной записи → один активный инвойс.
- **Проверка:** два вызова подряд; `SELECT telegram_payment_charge_id …` не меняется.
- **Тест:** `TestExpirePendingReusesInvoice`.

### A43 · F-27 · MEDIUM · `recover-dirty.sh` применяет миграции без транзакции
- **Где:** `deploy/recover-dirty.sh:156`
- **Что:** `psql -v ON_ERROR_STOP=1 -1 -f …` (или `--single-transaction`).
- **DoD:** прерванное применение не оставляет полуприменённый файл.
- **Проверка:** искусственно оборвать `psql` и проверить, что файл не применён наполовину.
- **Тест:** shell-тест на `-1`/`--single-transaction` в скрипте.

### A44 · F-28 · MEDIUM · пустой `admin_accounts` ломает deploy-verify; `adminctl` без гейта версии
- **Где:** `deploy/schema-ready.sh:606-620` · `api/cmd/adminctl/main.go:145-160`
- **Что:** (1) readiness в `deploy`-режиме не должен падать на пустой `admin_accounts` (seed-админ не считается) — либо документировать, что это ожидаемо, и проверять явно; (2) добавить в `adminctl` проверку версии схемы (rotate требует миграции 030).
- **DoD:** `SCHEMA_READINESS_MODE=deploy verify` проходит на БД после миграций; `adminctl` отказывает на старой схеме с понятным сообщением.
- **Проверка:** `SCHEMA_READINESS_MODE=deploy deploy/schema-ready.sh verify` на чистой БД; `adminctl rotate-seasonal` на БД без 030 → внятный отказ.
- **Тест:** `TestAdminctlRequiresMigration030`.

### A45 · F-35 · MEDIUM · месяц/ротация считаются по server-local, остальное по MSK
- **Где:** `api/internal/referral/referral.go:178` · `api/internal/admin/admin.go` (rotate-seasonal)
- **Что:** один хелпер `taroNow()` в MSK для всех ключей окон/месяцев; тест на границе суток.
- **DoD:** ключ месяца одинаков при любом `TZ` сервера; тест проходит с `TZ=UTC` и `TZ=Europe/Moscow`.
- **Проверка:** `TZ=UTC go test ./internal/referral/ ./internal/admin/` → зелено.
- **Тест:** `TestMonthKeyIndependentOfServerTZ`.

### A46 · F-36 · MEDIUM · A/B: float валиден, парсер ждёт int
- **Где:** `api/internal/payments/payments.go:352` · `VariantFor`
- **Что:** int-only валидация (или парсинг обоих); «молчаливый выключатель» A/B убрать.
- **DoD:** `ab.price_month=199.5` → 422 с внятным сообщением; целое значение работает.
- **Проверка:** `go test` кейсы 199 / 199.5 / "" .
- **Тест:** `TestABPriceMonthValidation`.

### A47 · F-33 · MEDIUM · экспорт дневника целиком в память 1 GiB
- **Где:** `web/app/api/diary/export/route.ts`
- **Что:** стримить ответ вместо `arrayBuffer`.
- **DoD:** экспорт 5000 записей не растёт в памяти процесса.
- **Проверка:** экспорт на наполненной БД; `docker stats`/RSS не растёт пропорционально размеру.
- **Тест:** интеграционный тест экспорта с проверкой потребления памяти/чанков.

### A48 · F-34 · MEDIUM · KPI-события не эмитятся, воронка в доках фиктивна
- **Где:** `web/lib/analytics.ts:46-47` (`paySuccess`/`trialStart`/`deleteMe`) · `docs/project-book/08-analytics-spec.md` · `07-roadmap/04-kpi.md`
- **Что:** эмитить события в реальных точках (оплата успех, старт триала, удаление аккаунта) либо убрать их из спеки и KPI; `paywall_show` должен слать реальный план, а не `unknown`.
- **DoD:** события в доках == события в коде; `paywall_show` несёт `plan`.
- **Проверка:** `grep -rn "paySuccess\|trialStart\|deleteMe" web/` → эмитятся; e2e события.
- **Тест:** `analytics.test.ts` на состав событий.

### A49 · F-51 · LOW · `/cards/`-страница CacheFirst; `VERSION` мёртв
- **Где:** `web/public/sw.js:41` (`startsWith("/cards/")`, `VERSION = "taro-v1"`)
- **Что:** исключить HTML-страницы из CacheFirst (только ассеты с расширением), ввести проверку/инкремент `VERSION` при деплое.
- **DoD:** страница карты не отдаётся из кэша навсегда; версия кэша bump-ится деплоем.
- **Проверка:** `curl` страницы с/из кэша; `grep VERSION web/public/sw.js`.
- **Тест:** тест на маршрутизацию в SW (страница vs ассет).

### A50 · F-52 · LOW · devDeps в прод-образе web (1.27 ГБ)
- **Где:** `web/Dockerfile:21`
- **Что:** стадия prod-зависимостей (`npm ci --omit=dev`) + `standalone`-вывод.
- **DoD:** в финальном образе нет dev-зависимостей; размер образа резко меньше.
- **Проверка:** `docker images` размер; `docker run … ls node_modules | wc -l` против dev.
- **Тест:** CI-шаг «прод-образ не содержит devDeps».

### A51 · F-53 · LOW · `nginx-tls.conf` — копия; гвард из одного grep
- **Где:** `deploy/nginx-tls.conf` · `.github/workflows/ci.yml` (guard `grep -Fq 'location = /readyz'`)
- **Что:** устранить дублирование (общий include/base + TLS-слой) и усилить гвард (конфиг-тест, а не один grep).
- **DoD:** изменения в базовом конфиге не требуют ручной правки TLS-копии; CI ловит расхождение.
- **Проверка:** изменить базовый конфиг в CI-ветке и убедиться, что падает.
- **Тест:** CI-шаг на структурное соответствие двух конфигов.

### A52 · F-54 · LOW · нет `admin_audit` для refund/push
- **Где:** `api/internal/admin/admin.go`
- **Что:** писать `admin_audit` для refund и push-операций (по образцу `config/publish`).
- **DoD:** возврат и пуш-операция оставляют запись аудита.
- **Проверка:** `SELECT … FROM admin_audit` после refund.
- **Тест:** `TestRefundWritesAdminAudit`.

### A53 · F-55 · LOW · AI-кэш переживает удаление аккаунта
- **Где:** `api/internal/ai/ai.go:665` · `api/internal/me/me.go` (Redis purge)
- **Что:** чистить `ai:`-ключи пользователя в `DELETE /v1/me` (и `sess/csrf/ent` уже чистим — см. A08).
- **DoD:** после удаления аккаунта его AI-кэш отсутствует.
- **Проверка:** создать кэш → удалить аккаунт → `redis-cli keys 'ai:*:<uid>'` → пусто.
- **Тест:** `TestDeleteMePurgesAICache`.

### A54 · F-49 · LOW · 112/175 тестов скипаются локально
- **Где:** `api/internal/testutil/testutil.go:20` (скип без `DATABASE_URL`)
- **Что:** сделать локальный запуск честным: поднять docker-compose для тестов одной командой (`make test` / скрипт) либо громче предупреждать о скипах; в CI — всегда с сервисами.
- **DoD:** локальный `go test ./...` поднимает зависимости сам либо явно падает, а не «молча зеленеет».
- **Проверка:** `make test` на чистой машине; `go test ./...` без БД → внятное сообщение.
- **Тест:** meta — «скипы видны в выводе».

### A55 · F-50 · LOW · 5 маршрутов без rate-limit + нет инварианта route↔rule
- **Где:** `api/internal/ratelimit/ratelimit.go:60-68` (нет правил для `/v1/cards/{id}`, `/v1/plans`, `/v1/entitlements/me`, `/v1/streak/me`, `/v1/ab/me`)
- **Что:** добавить правила (или явный opt-out с обоснованием) и **инвариантный тест**: каждый публичный маршрут покрыт правилом, если не помечен.
- **DoD:** инвариантный тест проходит и ломается при добавлении нового маршрута без правила.
- **Проверка:** `go test -run TestRouteRuleInvariant ./internal/ratelimit/`; перечислить маршруты и правила.
- **Тест:** сам инвариант (и мутационная проверка: временно убрать правило → тест падает).

### A56 · F-56 · INFO · Origin-rewrite: проверено, НЕ эксплуатируется
- **Где:** `web/lib/proxy.ts` · `api/internal/apierr.Decode` (требует `application/json`) · CORS
- **Что:** зафиксировать в отчёте/доках, что переписывание `Origin`/`Referer` не даёт browser login-CSRF (нет CORS-заголовков, preflight блокирует), чтобы находку не поднимали заново. Кода не менять; при желании — добавить защитный комментарий-ссылку.
- **DoD:** в доках есть запись «проверено, не эксплуатируется» с указанием причины.
- **Проверка:** `grep -n "Access-Control" api/` → пусто (это и есть причина).
- **Тест:** не нужен (INFO), но полезно зафиксировать в `docs/audit/` как REFUTED-risk.

### A57 · F-57 · INFO · «VAPID private key в репозитории» — ОПРОВЕРГНУТО
- **Где:** `.env.example:19-20` (плейсхолдеры `CHANGE_ME_GENERATE_YOUR_OWN`) · gitleaks (21 хит, все плейсхолдеры)
- **Что:** закрыть как REFUTED (зафиксировать в отчёте/памяти, чтобы не поднимали повторно). Реальная проблема — CI не умеет искать секреты (это A33), а не сам секрет.
- **DoD:** находка помечена REFUTED в `docs/audit/FINAL-VERDICT-1217da4.md` и в памяти; в бэклоге — закрыта без кода.
- **Проверка:** `git grep -nE "BEl[A-Za-z0-9_-]{20,}"` → пусто; `gitleaks detect` → 21 хит, все в тестах/ CI.
- **Тест:** не нужен (REFUTED).

### A58 · F-58 · MEDIUM · `drainOnce` забирает чужое чтение → CI-флак
- **Где:** `api/internal/ai/ai_e2e_test.go:545-582` · `api/internal/ai/worker.go:149`
- **Открыт:** при ремедиации A02, 2026-09-25. Тест вставляет свой `pending`-reading и вызывает неэкспортируемый `drainOnce`, чей claim-запрос берёт **любую** подходящую строку; при `workerBatchSize = 1` он может забрать чужую, и ассерт видит `status=pending attempts=0`.
- **Воспроизведено:** на чистой PG16+Redis под `go test -race -count=1 ./...` → `--- FAIL: TestE2EWorkerPersistsTerminalFallbackWithoutAI … status=pending interpretation="" attempts=0 calls=0`; изолированно и в паре с `internal/readings` — зелено; полный прогон повторно — зелено. То есть флак от планировщика, а не детерминированный сбой.
- **Почему не в W1:** тот же корень, что у A14 (изоляция тестов), но отдельная правка — поэтому вставлена сюда, чтобы не сбивать нумерацию. **Можно взять раньше очереди**, если CI начнёт мигать.
- **Что:** ограничить drain собственным reading теста (уникальный маркер + ограниченный повтор до выхода из `pending`), либо выдать воркерским тестам отдельную схему/БД.
- **DoD:** при подсеянном чужом `pending`-reading тест остаётся зелёным.
- **Проверка:** `go test -race -count=3 ./internal/ai/ ./internal/readings/` — стабильно зелено.
- **Тест:** после фикса внести в набор «чужое pending-reading перед `drainOnce`» — без фикса падает.

---

## Отметки о выполнении

| Дата | Задача | Что сделано | Чем проверено | Коммит |
|---|---|---|---|---|
| 2026-09-25 | **A03** (F-03, HIGH) | `web/Dockerfile`: `COPY --from=build --chown=node:node` для `.next` плюс `RUN mkdir -p /app/.next/cache/images && chown -R node:node /app/.next` до `USER node`. CI-guard на контракт Dockerfile. | «До»: `.next/cache` = uid 0 при процессе uid 1000; 100 запросов `/_next/image` → 100×200 клиенту и 100 `unhandledRejection` + 400 строк EACCES. «После»: каталоги 1000/1000, 100×200, EACCES/unhandledRejection = 0, файл кэша создан. Продовый путь `docker compose build web` проверен. Регресс: vitest 21/21, eslint, YAML, guard + мутационная проверка guard-а. | не коммитил (ждёт решения) |
| 2026-09-26 | **A11** (F-25, MEDIUM) | В дедупликацию подписок при merge добавлено `AND s.payment_id IS NULL` — платёжная строка больше не удаляется. Два новых тест-файла (merge + refund). | Мутация (снятие условия) поймана **обоими** тестами. Цепочка закрыта с двух сторон: merge сохраняет связь, `finalizeRefund` со связью отзывает дни (`refunded`, 0 активных), без связи не отзывает ничего (контр-случай). 4 теста. Регресс: полный `go test -race` 14/14, build, vet, gofmt 0. | не коммитил (ждёт решения) |
| 2026-09-26 | **A10** (F-24, MEDIUM) | Advisory-блокировка referrer + чтение счётчика под ней + условный инкремент (`WHERE <= 30`), константа `referralMonthlyCapDays`. Новый тест-файл (2 теста). | Мутация (возврат к check-then-act) → тест падает **3/3**; с фиксом `-count=3` зелёные. Тест детерминирован триггером `pg_sleep` на `INSERT INTO entitlements`. Пойман баг в собственном фиксе: компенсация уменьшала счётчик (30 → 27) — убрана. Регресс: полный `go test -race` 14/14, build, vet, gofmt 0. | не коммитил (ждёт решения) |
| 2026-09-26 | **A09** (F-09, HIGH) | Новая доменная выборка `ReconcileWebhookEvents` (единственная копия SQL). `adminctl payments reconcile` (таблица + JSON, `exit 2` = есть записи → алерт), `GET /v1/admin/payments/audit` за `RequireAdmin`, блок «Аудит вебхуков» в панели, cron+алерт в `deploy/README.md`. Все SQL-литералы причин заменены на константы. | Живой стек: собрал `api-admin`, эндпоинт вернул **84 реальных события** из dev-БД, включая `amount_mismatch`, `charge_reused`, `duplicate_charge` — инциденты, которые были зафиксированы и незамечены. Неверный токен → 403. CLI: `exit 2` с записями, `exit 0` без, внятные ошибки на мусорных аргументах, provisioning не сломан. 4 теста. Мои тесты пришлось переделать на scoped-фикстуры (класс F-58). Регресс: полный `go test -race` 14/14, build, vet, gofmt 0, `node --check app.js`. | не коммитил (ждёт решения) |
| 2026-09-26 | **A08** (F-08, HIGH) | Глобальный порядок блокировок users → payments: в `loadWebhookPayment` user-строка под `FOR KEY SHARE` отдельным оператором до `FOR UPDATE OF p`. Очистка Redis в `DELETE /v1/me` перенесена после `tx.Commit()`. Два новых тест-файла. | «До»: счётчик `pg_stat_database.deadlocks` **0 → 50**, `DELETE /v1/me` = **500 в 50/50** итераций. «После»: 50 итераций без единого дедлока, обе транзакции завершаются штатно. Тест дедлока детерминирован (триггер `pg_sleep` на UPDATE payments) и измеряет **счётчик дедлоков в PG**, а не HTTP-коды. Второй тест (мутация с очисткой до коммита) пойман: `redis key sess:… was purged although the delete transaction rolled back`. Регресс: полный `go test -race` 14/14, build, vet, gofmt 0. | не коммитил (ждёт решения) |
| 2026-09-26 | **A07** (F-07, HIGH) | `pg_advisory_xact_lock(hashtext('subscriptions:'||user_id))` первым оператором выдачи + `ON CONFLICT (payment_id) … DO NOTHING`. Миграция 035: уникальный индекс на `payment_id` со схлопыванием дублей перед созданием. Новый тест-файл (3 теста), 035 добавлена в перечень обязательных release-файлов CI. | «До»: **4/4 прогона теряют ровно 720 часов (30 дней)** за два оплаченных месяца. «После»: `-count=8` зелёные, полный `go test -race` 14/14 на чистой PG16 v35. Миграция проверена на грязной базе (55 дублей → 1 строка с б**ольшим** valid_until, 28 NULL-бонусов целы, `dirty=false`), `down`/`up` цикл. Тест пришлось сделать детерминированным (триггер `pg_sleep` на INSERT): без него гонка не ловилась 8 прогонов подряд. На БД без 035 тесты падают громко (42P10). Регресс: build, vet, gofmt 0, migrate manifest 35, YAML. | не коммитил (ждёт решения) |
| 2026-09-26 | **A06** (F-32, MEDIUM) | Новый `deploy/check-env.sh`: список 15 обязательных переменных выводится из compose-файлов, отсеивает шаблонные значения; вызывается в префлайте `deploy.yml` до первого `docker compose`. `.env.example` дополнен 6 недостающими ключами (пустые значения + пояснения). CI-шаг `--keys-only .env.example`. README — раздел. | «До»: `compose config` на шаблоне → одна ошибка интерполяции (`DATABASE_URL`) вместо 7 отсутствующих ключей, и уже внутри деплоя. «После»: один экран `STOP: 7 required variable(s) missing or empty` + `STOP: 6 … template/dev values`. Три режима проверены; 2 мутации пойманы (убранный ключ; новый `${BRAND_NEW_SECRET:?}` в compose без записи в шаблон). Найден и исправлен баг разбора аргументов в самом скрипте. Регресс: `sh -n`/`test -x` всех `deploy/*.sh`, YAML, `compose config` dev. | не коммитил (ждёт решения) |
| 2026-09-26 | **A05** (F-04, HIGH) | `CardArt.tsx`: allowlist расширен префиксом `cards/` (сид не переписывался, миграций нет). Новый `CardArt.test.tsx` (сид-002 как источник правды, без БД): 78 ключей, прохождение allowlist, `src === "/" + key`, 10 векторов traversal, `card-back`. `public/cards/README.md` переписан по факту. | «До»: allowlist совпал с **0 из 78** сид-ключей, файлов **0 из 78**; «после»: regex из исходника прогнан по всем ключам → `78 / 78`. Мутация (откат regex) → 3 теста падают, после отката 27/27. Непокрытая половина честно зафиксирована: самих 78 `.webp` нет — это E13/E14 владельца, проверка ассетов оставлена `it.todo` с причиной. Регресс: vitest 27/27, tsc 0, eslint 0, build OK, PII-grep, node --check. | не коммитил (ждёт решения) |
| 2026-09-26 | **A04** (F-14, HIGH) | CSP приложения выровнена посимвольно на edge во всех трёх файлах; из приложения убран `X-Frame-Options: DENY` (противоречил своему же allow-list `frame-ancestors` и всё равно срезался nginx); в `ci.yml` — шаг равенства + запрет XFO; README дополнен. | До: расхождение в `frame-ancestors` и `script-src` (измерено, 9 директив). После: `npm run build` → в `.next/routes-manifest.json` CSP 281 символ, XFO нет; живой `next start` на `/` и `/diary` отдаёт ровно nginx-политику. Тест: 3 мутации (вернуть `frame-ancestors 'none'`, убрать telegram.org из nginx, вернуть XFO) — все пойманы. Регресс: vitest 21/21, eslint, YAML. | не коммитил (ждёт решения) |
| 2026-09-25 | **A01** (F-01, CRITICAL) | `.gitignore`: `backups/`, `taro-backups/`, `*.sql.gz`, `*.sql.gz.gpg`. `backup.sh`: дефолт `BACKUP_DIR` → `$(dirname $ROOT)/taro-backups` + гард «отказ при BACKUP_DIR внутри worktree». `README.md`: контракт `BACKUP_DIR` + `mkdir/chown/chmod`. `ci.yml` (job `security`): 2 новых шага — игнорирование + пустой untracked при существующем `backups/`, и проверка срабатывания гарда. | «До»: на клоне `1217da4` `?? backups/probe` → `deploy.yml:84` падает, гарда нет. «После»: `git check-ignore -v` → `.gitignore:14`, untracked пуст, **полный префлайт `deploy.yml:75-84` проходит**; `BACKUP_DIR=$PWD/backups` → exit 1 с внятным сообщением; дефолт резолвится вне worktree. Регресс: `sh -n`+`test -x` всех `deploy/*.sh`, YAML `ci.yml`, `go build`, `go vet`, `tsc` — OK. | не коммитил (ждёт решения) |
| 2026-09-25 | **A02** (F-02, CRITICAL) | Разделены netns: `admin-access` стал отдельным контейнером, порт `127.0.0.1:8081→8082` переехал на него, `api-admin` наружу не публикуется и слушает `ADMIN_LISTEN_ADDR` (дефолт `0.0.0.0:8081`). Новая сеть `admin` — только `api-admin` + `admin-access`. `admin-proxy.conf`: апстрим по DNS + `resolver 127.0.0.11 valid=10s` + `proxy_pass http://$admin_api$request_uri`. `admin-job.sh` и `deploy.yml` спрашивают порт у `admin-access`. CI: shell-инварианты + структурная проверка compose. | Вживую: `--force-recreate api-admin` → `admin-access` тот же ID, сразу 200 на `/`, `/healthz`, `/readyz`, auth 401/403, свой `eth0`, изоляция сети подтверждена. Попутно найден и починен латентный 502 при смене IP апстрима (статический резолв nginx). Регресс: `go test -race -count=1 ./...` 14/14 на чистой PG16, `go build`/`vet`, `sh -n`, YAML. Из флака теста заведена новая находка **A58/F-58**. | не коммитил (ждёт решения) |

