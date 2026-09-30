# Deploy / VPS

## Конфигурация

`docker-compose.yml` — локальный/dev-контур. В нём оставлены только dev-значения для удобства `.env.example`; production workflow никогда не запускает этот файл без `deploy/docker-compose.prod.yml`.

Для production override используется Docker Compose v2.24+ (поддерживает `!reset`/`!override`).

Production-команда для HTTP-терминации на внешнем LB:

```sh
COMPOSE=(docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml)
"${COMPOSE[@]}" config --quiet
```

Tag deployment использует native TLS override и требует `TLS_CERT_DIR` в `.env`:

```sh
TLS_CERT_DIR=/etc/letsencrypt/live/example.com \
  "${COMPOSE[@]}" -f deploy/docker-compose.tls.yml config --quiet
```

Production override требует настоящие значения и не содержит dev-секретов. В `.env` на VPS задаются как минимум:

- `DATABASE_URL` с хостом `db` внутри Compose, либо `%`-encoded URL для внешней БД;
- `POSTGRES_PASSWORD`, `REDIS_PASSWORD`, `REDIS_ENC_KEY` (64 hex-символа);
- `OPENROUTER_API_KEY`, `OPENROUTER_API_KEY_2`;
- `JWT_SECRET`, `TG_BOT_TOKEN`, `TG_STARS_SECRET_TOKEN`;
- `VAPID_PUBLIC_KEY`, `VAPID_PRIVATE_KEY`;
- `ADMIN_API_TOKEN`;
- `ADMIN_ORIGIN`, `PUBLIC_ORIGIN`;
- `BACKUP_GPG_KEY` для production backup;
- `TLS_CERT_DIR` для tag deployment с native TLS;
- `NEXT_PUBLIC_BASE_URL`.

Полный список не хранится вручную: он выводится из самих compose-файлов (`${VAR:?…}`) скриптом `deploy/check-env.sh`, который вызывается в префлайте `deploy.yml` **до** первого `docker compose`. Без этого compose сообщает об одной отсутствующей переменной за прогон, и нехватка ключа всплывает сырой ошибкой интерполяции посреди деплоя.

```sh
deploy/check-env.sh                      # проверить .env (значения + отсев шаблонных)
deploy/check-env.sh --keys-only .env.example   # только наличие ключей (так проверяет CI)
deploy/check-env.sh /path/to/other.env    # другой файл
```

Скрипт отличает «не задано» от «осталось шаблонным»: значения `CHANGE_ME*`, `dev-only-*` и `taro_dev_only` не считаются заданными, поэтому копия `.env.example` без правок не пройдёт префлайн. Полный `compose config` для prod+tls в CI — задача A33.

`ADMIN_ORIGIN` не является секретом: это точное origin браузера для mutation-запросов панели. Production override требует его явно. В локальном Compose значение по умолчанию `http://127.0.0.1:${ADMIN_HOST_PORT:-8081}`, поэтому кастомный loopback-порт остаётся согласованным. Для отдельного production-домена или защищённого endpoint задайте `ADMIN_ORIGIN` явно.

`api-public` принимает `AI_WORKER_MAX_ATTEMPTS` (1–20, по умолчанию 5), `AI_WORKER_BACKOFF_SECONDS` (1–3600, по умолчанию 30), `AI_WORKER_MAX_BACKOFF_SECONDS` (1–86400, по умолчанию 900), `AI_WORKER_TICK_SECONDS` (1–60, по умолчанию 2) и `AI_WORKER_BATCH_SIZE` (1–50, по умолчанию 10). `CRISIS_PATTERNS_JSON` ожидает JSON-массив строк, а `CRISIS_RESOURCE_TEXT` — текст ресурса; пустые значения оставляют DB/default policy. Эти переменные не содержат секретов, но production override передаёт их отдельно от `api-admin`.

`AI_WORKER_TICK_SECONDS` — период ожидания **пустой** очереди, а не темп обработки: при непустой очереди воркер дренирует её немедленно, поэтому первое чтение после простоя ждёт не больше тика. `AI_WORKER_BATCH_SIZE` — сколько чтений захватывается за один drain, и он же ограничивает параллелизм генерации. До A15/F-06 обе величины были жёстко зашиты (`batch=1`, `tick=30s`) и давали потолок **2.00 readings/min** — одно чтение за 30 секунд, независимо от состояния очереди. Одно осознанное следствие: `AI_WORKER_BATCH_SIZE` умножается на число одновременных запросов к OpenRouter (по умолчанию до 10). При ограниченном rate-limit провайдера снижайте batch, а не увеличивайте тик.


`DATABASE_URL` не должен указывать на `localhost` внутри контейнеров. Если пароль содержит специальные символы, используйте URL-encoding. Файл `.env` должен иметь режим `600`; не помещайте его в git или backup-архив.

## Redis: почему `maxmemory-policy noeviction`

Инстанс `cache` общий для ключей лимитов (`rl:*`), сессий (`sess:*`, `csrf:*`), entitlement-счётчиков (`ent:*`) и bulk-кэша ответов AI (`ai:cache:*`, TTL 7 суток). Политика `allkeys-lru` при достижении `maxmemory` вытесняла **любой** ключ, включая `rl:*`: окно rate-limit молча обнулялось, и клиент получал новый бюджет запросов. Это подтверждено замером — после ballast-заливки до `maxmemory` ключ `rl:/v1/spreads:ip:…` исчезал (`EXISTS` → 0).

Поэтому политика инстанса — `noeviction`, а источник давления памяти ограничен: `AI_CACHE_MAX_ENTRIES` (по умолчанию 5000 записей) и обрезка самых старых записей при превышении. Следствие `noeviction` — при нехватке памяти запись возвращает OOM-ошибку, а не вытесняет ключи: `failClosed`-маршруты (`/v1/auth/*`, `/v1/readings`, `/v1/admin/*`, `/v1/referral/*`, `/v1/payments/*`, `/v1/diary`, `/v1/push/*`, `/v1/me`) отдают 503, и это осознанный размен «лимиты важнее доступности».

## Redis: две роли (A17/F-43)

| роль | инстанс | что лежит | `maxmemory` | политика | пароль |
| --- | --- | --- | --- | --- | --- |
| `critical` | `cache` | `rl:*`, `sess:*`, `csrf:*`, `ent:*` | 200 МБ (`mem_limit` 384m) | `noeviction` | `REDIS_PASSWORD` |
| `ai_cache` | `cache-ai` | `ai:cache:*` (TTL 7 суток) | 128 МБ (`mem_limit` 192m) | `volatile-ttl` | `REDIS_AI_PASSWORD` |

`api-public` и `api-admin` получают обе роли через `REDIS_AI_ADDR` / `REDIS_AI_PASSWORD`. Если `REDIS_AI_ADDR` не задан, API печатает предупреждение на старте и кэш делит инстанс `critical` — деградация заметная, а не тихая. Формат `REDIS_AI_ADDR` допускает и отдельную БД того же инстанса (`host:6379/1`) — этим пользуются тесты, чтобы разделение ключей было проверяемо, а не только задумано.

`cache-ai` намеренно не публикует портов и подключён только к сети `internal`: наружу торчит лишь `admin-access`/`nginx`.

Наблюдение за ролями: `GET /v1/admin/cache` (за `RequireAdmin`) отдаёт `cache_bytes{role}` в единственной форме, которую этот проект может себе позволить без prometheus — число ключей (точное) и байты (выборочная оценка через `MEMORY USAGE ... SAMPLES 1`, поле `approx`). Роли считаются раздельно: `ai_cache` и `critical`.

Расчёт бюджета: 5000 записей × ~2 КБ ≈ 10 МБ при бюджете инстанса 128 МБ, поэтому до вытеснения дело не доходит. TTL 7 суток при потолке воркера 10 чтений / 2 с (до 300 чтений/мин) означает, что **связывает именно бюджет, а не TTL**: без обрезки кэш рос бы неделями. Обрезка срабатывает при превышении и оставляет 9/10 бюджета, поэтому случается не на каждой записи.

При ротации VAPID меняйте пару `VAPID_PUBLIC_KEY`/`VAPID_PRIVATE_KEY` одновременно, пересоздавайте API-контейнеры и просите клиентов повторно opt-in push; старые подписки нельзя считать валидными после смены applicationServerKey.

Локальная разработка использует текущий `.env.example` с `POSTGRES_PASSWORD`: Compose и `migrate.sh` сами строят URL для `db`. Явный `DATABASE_URL` можно добавить в локальный `.env`, если нужна другая БД. Если `NEXT_PUBLIC_BASE_URL` не задан, локальная сборка web использует безопасный `http://localhost:3000`; production всё равно требует явное значение.

## Миграции и readiness

```sh
docker compose up -d --wait db cache
./deploy/migrate.sh up
./deploy/migrate.sh verify
```

Для production перед командами миграций задаётся `DEPLOY_ENV=prod`:

```sh
DEPLOY_ENV=prod ./deploy/migrate.sh up
DEPLOY_ENV=prod ./deploy/migrate.sh verify
```

Production tag deployment останавливает и дренирует старые `web`, `api-public` (включая встроенный worker), `api-admin` и proxy перед применением миграций, затем запускает новый stack. Только после provisioning администратора выполняется строгая проверка `SCHEMA_READINESS_MODE=deploy`; отсутствие активного admin account останавливает релиз.

`migrate.sh` принимает `up`, `verify`, ручной `down [steps]` и диагностическую команду `manifest`. Перед запуском любой операции проверяется непрерывность числовых версий 001…latest и наличие пары `.up.sql`/`.down.sql` для каждой версии. Отдельная проверка файлов запускается так:

```sh
./deploy/migrate.sh manifest
```

`down` дополнительно требует `ALLOW_DOWN=1` и подтверждение `YES`. После подтверждения скрипт сначала читает текущую версию и проверяет целевой диапазон в контейнере `schema-check`; только после успешного preflight запускается `migrate down`. Переход через forward-only 027/028/031/032/033/034, через 030 с ротированными `session_version`, или через 029 с заполненными платежами/подписками/audit-строками отклоняется до удаления любой миграции. Для подтверждённого отката 029 миграционный контейнер получает session-scoped `PGOPTIONS` с `taro.migration_029_preflight=confirmed`; это подтверждение не записывается в глобальное окружение. Перед backup и ручным down сначала сохраните дамп.

Скрипт монтирует SQL только в режиме `:ro`, запускает migration-контейнер с read-only filesystem, без capabilities и с `no-new-privileges`. После `up` он проверяет через `schema-ready.sh`:

- наличие `schema_migrations`, `dirty=false` и точное совпадение с последней версией из `api/migrations`;
- таблицы и ограничения 027–033, включая `admin_accounts.session_version` с validated positive check;
- `readings.quota_state` и его validated check/index, repair table 031 и её FK;
- authorization receipts 032, их primary key/FK/index state, terminal-quota function/trigger и поведенческие пробы primary key/trigger;
- `payment_webhook_events`, audit primary/unique/check/index, отсутствие FK к `payments`, а также включённый для обычных сессий `payments_snapshot_guard` с полным набором snapshot-колонок;
- migration 034: `reading_quota_quarantine_034`, его primary key/index, `reading_terminal_quota_guard_034()` и trigger insert/update, включённый для обычных сессий; поведенческая проверка должна отклонять непустой terminal-результат без `allowed` quota и разрешать корректный `allowed`-результат.

По умолчанию `SCHEMA_READINESS_MODE=schema`: проверка нужна сразу после миграций, поэтому допускает `active admin accounts: 0`, но явно выводит, что provisioning ещё требуется. После создания администратора выполните строгую проверку:

```sh
SCHEMA_READINESS_MODE=deploy DEPLOY_ENV=prod ./deploy/migrate.sh verify
```

В режиме `deploy` нужна хотя бы одна активная запись `admin_accounts`, связанная с активным пользователем, у которого `role='admin'`; при значении `0` команда завершается с ошибкой и не сообщает о deploy readiness. Migration 033 удаляет старый cascade FK из `payment_webhook_events` для уже применённого 029; её down отказывается откатывать retention boundary. Migration 030 также отказывается удалять `session_version`, если generation уже была ротирована.

При `DATABASE_URL_FILE` файл содержит одну строку с URL. `DATABASE_URL` и `DATABASE_URL_FILE` нельзя задавать одновременно: при конфликте скрипт завершается до запуска Compose. Скрипт создаёт случайный временный env-файл с правами `600`, удаляет его через trap и не передаёт URL в аргументах `docker compose`.

### Восстановление legacy dirty-схемы

Если `schema_migrations` содержит ровно `version=1, dirty=true`, а таблицы 001–016 уже существуют, обычный `migrate.sh up` блокируется намеренно. Для этой подтверждённой legacy-формы есть отдельная процедура:

```sh
ALLOW_REPAIR=1 BACKUP_FILE=/secure/path/taro-before-repair.dump ./deploy/recover-dirty.sh
```

Процедура сначала проверяет состояние `version=1, dirty=true`, непрерывный manifest и пары `.up.sql`/`.down.sql`, а также legacy-структуру и seed: обязательные таблицы, ровно 78 непрерывных карт, планы `free`/`month_299`/`year_2490`/`single_99`/`trial_3d`/`referral_bonus`, семь раскладов, девять обязательных ключей `app_config` и seed-пользователя `00000000-0000-0000-0000-000000000001` с ролью `admin`. Только после этого preflight скрипт делает приватный dump, применяет миграции после legacy-версии, фиксирует найденную последнюю версию и запускает `schema-check`. `recover-dirty.sh` принимает только локальный Compose `db` и отказывается работать с `DATABASE_URL` или `DATABASE_URL_FILE`, чтобы не проверить одну БД и изменить другую. Если обнаружена любая другая схема, скрипт отказывается менять её; нужно сначала провести ручной schema diff. После repair перезапустите API, создайте администратора и проверьте `schema_migrations`.

## Сети и admin

`web` подключён только к `edge` и не имеет доступа к `db` или `cache`. В dev БД и Redis доступны хосту только через `127.0.0.1`; отдельная `dev-access` сеть не выдаётся web-контейнеру. Production override убирает host-порты БД и Redis. Старая сеть `internal` сохраняет совместимость при upgrade и не меняет membership-check; полная изоляция web обеспечивается именно отсутствием web в этой сети.

Admin-сегмент изолирован в собственной сети `admin` (`docker-compose.yml`): в ней ровно два участника — `api-admin` и `admin-access`. `api-admin` слушает `ADMIN_LISTEN_ADDR` (по умолчанию `0.0.0.0:8081`), **не публикует ни одного host-порта** и доступен только из `admin`; `api-public` в этой сети не состоит, поэтому достучаться до admin-API по сети нельзя. `admin-access` — отдельный контейнер: его nginx слушает `8082`, публикует host-порт только на `127.0.0.1:${ADMIN_HOST_PORT:-8081}` и ходит в апстрим по DNS-имени `api-admin:8081`.

Два свойства, которые нельзя откатывать:

* **Общего network namespace больше нет.** При `network_mode: service:api-admin` пересоздание `api-admin` (деплой, краш, `compose up -d`) оставляло `admin-access` в осиротевшем namespace без `eth0` — админка и все четыре cron-задачи (`admin-job.sh`) умирали до ручного пересоздания. Обратно не возвращать: инвариант проверяется в `ci.yml`.
* **nginx резолвит апстрим на каждый запрос** (`resolver 127.0.0.11 valid=10s` + `proxy_pass http://$admin_api$request_uri`). Со статическим `proxy_pass http://api-admin:8081` адрес резолвится один раз на старте, и после пересоздания `api-admin` с новым IP `admin-access` отдаёт 502 до ручного рестарта — та же по симптому поломка. Из-за переменной в `proxy_pass` исходный URI нужно передавать явно через `$request_uri`.

Канонический origin панели по умолчанию — `http://127.0.0.1:8081` и автоматически следует за `ADMIN_HOST_PORT`; при другом origin задайте `ADMIN_ORIGIN`. Поэтому работают без публичного доступа:

```sh
curl --fail http://127.0.0.1:8081/healthz
ssh -L 8081:127.0.0.1:8081 user@VPS
```

После открытия туннеля admin доступен на `http://127.0.0.1:8081`. На этом же loopback-порту отдаётся статическая панель Taro Control; `/` — UI, `/healthz` — healthcheck, `/v1/admin/*` — API.

Панель использует логин и пароль, проверяет bcrypt-хэш в `admin_accounts` и выдаёт HttpOnly `taro_admin` cookie. `ADMIN_API_TOKEN` предназначен для cron/server-to-клиентских вызовов и не должен попадать в браузер.

Пароль администратора provisioning-ом без записи секрета в историю shell:

```sh
password="$(openssl rand -base64 48 | tr -dc 'A-Za-z0-9' | cut -c1-28)"
printf '%s\n' "$password" | docker compose run --rm --no-deps --entrypoint /adminctl api-admin -username test-admin
printf 'password=%s\n' "$password"
unset password
SCHEMA_READINESS_MODE=deploy DEPLOY_ENV=prod ./deploy/migrate.sh verify
```

Команда читает пароль из stdin, хеширует его bcrypt и связывает аккаунт с seed-пользователем-администратором. Для другого пользователя передайте его UUID через `-user-id`.

```sh
docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml port admin-access 8082
ss -ltn
```

Ожидается только `127.0.0.1:8081`; `0.0.0.0:8081` и `[::]:8081` не допускаются.

## Cron

Cron не должен получать токен из неэкспортированной переменной. Используйте wrapper, который проверяет loopback health и выполняет запрос внутри admin-контейнера, где токен уже находится в его environment:

```cron
0 4 * * * DEPLOY_ENV=prod /opt/taro/deploy/backup.sh >>/var/log/taro-backup.log 2>&1
5 0 * * * DEPLOY_ENV=prod /opt/taro/deploy/admin-job.sh /v1/admin/rotate-seasonal >>/var/log/taro-admin.log 2>&1
5 21 * * * DEPLOY_ENV=prod /opt/taro/deploy/admin-job.sh /v1/admin/push-evening >>/var/log/taro-admin.log 2>&1
30 21 * * * DEPLOY_ENV=prod /opt/taro/deploy/admin-job.sh /v1/admin/push-streak-risk >>/var/log/taro-admin.log 2>&1
0 9 * * * DEPLOY_ENV=prod /opt/taro/deploy/admin-job.sh /v1/admin/remind-expiring >>/var/log/taro-admin.log 2>&1
```

`admin-job.sh` разрешает только четыре утверждённых endpoint-а и использует `flock`.

### Аудит вебхуков (A09/F-09)

`payment_webhook_events` писалась всегда, но читателей не было: дубликаты списаний и расхождения вебхуков фиксировались и оставались незамеченными. Теперь их читают CLI, админ-API и панель.

```sh
# читать вручную
printf '%s' "$ADMIN_API_TOKEN" | docker compose run --rm --no-deps -T -e ADMIN_API_TOKEN api-admin \
  payments reconcile --since 24h
# варианты: --all (вся история), --json (для мониторинга), --limit N
```

Код возврата: `0` — расхождений нет, `1` — ошибка запуска, `2` — есть записи, требующие внимания (`owner_unverified` считается нормой: это первый вебхук без привязанного Telegram). На этом строится алерт, пока в проекте нет метрик (F-20):

```cron
*/30 * * * * cd /opt/taro && printf '%s' "$ADMIN_API_TOKEN" | docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml run --rm --no-deps -T -e ADMIN_API_TOKEN api-admin payments reconcile --since 1h >>/var/log/taro-webhook-audit.log 2>&1; [ $? -ne 2 ] || printf '%s\n' "webhook audit: records need review" | mail -s "TARO webhook audit" ops@example.com
```

Тот же отчёт доступен админ-API `GET /v1/admin/payments/audit?since=&limit=&all=1` (за `RequireAdmin`) и в панели Taro Control, раздел «Платежи» → «Аудит вебхуков».

## Backup

`backup.sh` executable, использует `flock`, временный приватный plaintext-файл, проверяет gzip и ротирует `.gz`/`.gpg`. В production `BACKUP_GPG_KEY` обязателен; при ошибке шифрования plaintext удаляется. Скрипт снимает дамп с Compose-сервиса `db`; для внешней БД нужен отдельный проверенный backup-процесс. На локальной машине отсутствие GPG-ключа только печатает предупреждение.

`BACKUP_DIR` по умолчанию — соседняя с checkout директория `../taro-backups` (при checkout в `/opt/taro` это `/opt/taro-backups`). Скрипт **отказывается работать**, если `BACKUP_DIR` оказался внутри worktree: `deploy.yml` отвергает релиз, если в дереве есть любой untracked-файл, поэтому бэкап внутри репозитория блокирует все дальнейшие деплои. Каталог должен существовать и быть writable для пользователя cron, иначе скрипт завершится с ошибкой на `mkdir`:

```sh
mkdir -p /opt/taro-backups && chown deploy:deploy /opt/taro-backups && chmod 700 /opt/taro-backups
# либо задать свой путь в crontab:
# 0 4 * * * DEPLOY_ENV=prod BACKUP_DIR=/var/backups/taro /opt/taro/deploy/backup.sh >>/var/log/taro-backup.log 2>&1
```

В `.gitignore` также добавлены `backups/`, `taro-backups/`, `*.sql.gz`, `*.sql.gz.gpg` — это вторая линия защиты, если оператор всё же переопределит `BACKUP_DIR` внутрь репозитория в обход проверки.

## TLS и reverse proxy

Базовый `deploy/nginx.conf` слушает HTTP и предназначен для варианта, когда TLS завершает certbot на хосте или внешний load balancer. Для HTTP HSTS намеренно не добавляется.

Если TLS завершает сам nginx, tag workflow всегда применяет override с каталогом `fullchain.pem` и `privkey.pem`; `TLS_CERT_DIR` должен быть задан в `.env`:

```sh
TLS_CERT_DIR=/etc/letsencrypt/live/example.com \
  docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml -f deploy/docker-compose.tls.yml up -d
```

`deploy/nginx-tls.conf` включает TLS 1.2/1.3, HSTS, безопасные proxy headers и проксирует как `/healthz`, так и `/readyz` на public API. Сертификаты неизвестны этому репозиторию, поэтому native TLS не включается автоматически локальной/dev-командой. Если TLS завершает LB, используйте только base+prod override; LB должен быть единственным доверенным источником `X-Forwarded-Proto: https`; blindly trusting this header from the public port недопустимо. Перед production необходимо проверить реальный LB, `Secure` cookies и trusted proxy IP list.

Nginx limits now apply to both `/v1/*` and the actual Next.js `/api/*` routes. If an external LB is used, configure `real_ip` only for its fixed CIDRs; otherwise `$binary_remote_addr` can collapse all users into one bucket. SSE reading routes `/v1/readings` and the browser-facing `/api/readings` use HTTP/1.1, disabled buffering and bounded timeouts. The CSP allows framing only from the known Telegram Web origin `https://web.telegram.org`; `X-Frame-Options: DENY` is intentionally absent because it would override that allowance. The native Telegram WebView origin was not independently verifiable here and must be checked before broadening the allowlist.

**CSP не дублируется, а совпадает.** Edge делает `proxy_hide_header Content-Security-Policy` и отдаёт свою копию, поэтому политика в `web/next.config.js`, `deploy/nginx.conf` и `deploy/nginx-tls.conf` обязана быть **посимвольно одинаковой** — иначе правка в приложении молча исчезает в проде, а правка на edge расходится с dev. Равенство проверяет шаг `ci.yml` (job `security`): он сравнивает все три строки, требует `frame-ancestors 'self' https://web.telegram.org` и запрещает объявлять `X-Frame-Options` в приложении — у него нет allow-list формы, и `DENY`/`SAMEORIGIN` заломали бы фрейминг Telegram WebView. Правьте все три места вместе.

## Релиз и CI

CI pins actions and images by immutable SHA/digest, builds clean API/admin images, syntax-checks the admin UI, validates the migration manifest, mounts migrations read-only, checks that required adminctl/UI/migration files are tracked, and refuses dirty or incomplete schemas before Go E2E tests.

The tag workflow starts only after the `ci` workflow succeeds, validates a semantic `vMAJOR.MINOR.PATCH` tag, fetches that exact ref, checks out a detached commit, uses the production+native-TLS override, runs migration readiness, waits for Compose health, checks the actual public HTTPS `/readyz` body, checks the actual admin loopback `/readyz`, and fails if the admin binding is not loopback. There is no fallback from `/readyz` to `/`.

The release checkout is intentionally detached. A VPS workspace with tracked changes is rejected; keep runtime state outside the repository and do not force-push tags.
