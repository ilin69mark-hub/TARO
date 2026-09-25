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

`ADMIN_ORIGIN` не является секретом: это точное origin браузера для mutation-запросов панели. Production override требует его явно. В локальном Compose значение по умолчанию `http://127.0.0.1:${ADMIN_HOST_PORT:-8081}`, поэтому кастомный loopback-порт остаётся согласованным. Для отдельного production-домена или защищённого endpoint задайте `ADMIN_ORIGIN` явно.

`api-public` принимает `AI_WORKER_MAX_ATTEMPTS` (1–20, по умолчанию 5), `AI_WORKER_BACKOFF_SECONDS` (1–3600, по умолчанию 30) и `AI_WORKER_MAX_BACKOFF_SECONDS` (1–86400, по умолчанию 900). `CRISIS_PATTERNS_JSON` ожидает JSON-массив строк, а `CRISIS_RESOURCE_TEXT` — текст ресурса; пустые значения оставляют DB/default policy. Эти переменные не содержат секретов, но production override передаёт их отдельно от `api-admin`.

`DATABASE_URL` не должен указывать на `localhost` внутри контейнеров. Если пароль содержит специальные символы, используйте URL-encoding. Файл `.env` должен иметь режим `600`; не помещайте его в git или backup-архив.

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

`api-admin` слушает `127.0.0.1:8081` внутри контейнера. `admin-access` использует тот же network namespace, слушает `8082`, а host-порт проксирует только на `127.0.0.1:${ADMIN_HOST_PORT:-8081}`. Канонический origin панели по умолчанию — `http://127.0.0.1:8081` и автоматически следует за `ADMIN_HOST_PORT`; при другом origin задайте `ADMIN_ORIGIN`. Поэтому работают без публичного доступа:

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
docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml port api-admin 8082
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

## Backup

`backup.sh` executable, использует `flock`, временный приватный plaintext-файл, проверяет gzip и ротирует `.gz`/`.gpg`. В production `BACKUP_GPG_KEY` обязателен; при ошибке шифрования plaintext удаляется. Скрипт снимает дамп с Compose-сервиса `db`; для внешней БД нужен отдельный проверенный backup-процесс. На локальной машине отсутствие GPG-ключа только печатает предупреждение.

## TLS и reverse proxy

Базовый `deploy/nginx.conf` слушает HTTP и предназначен для варианта, когда TLS завершает certbot на хосте или внешний load balancer. Для HTTP HSTS намеренно не добавляется.

Если TLS завершает сам nginx, tag workflow всегда применяет override с каталогом `fullchain.pem` и `privkey.pem`; `TLS_CERT_DIR` должен быть задан в `.env`:

```sh
TLS_CERT_DIR=/etc/letsencrypt/live/example.com \
  docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml -f deploy/docker-compose.tls.yml up -d
```

`deploy/nginx-tls.conf` включает TLS 1.2/1.3, HSTS, безопасные proxy headers и проксирует как `/healthz`, так и `/readyz` на public API. Сертификаты неизвестны этому репозиторию, поэтому native TLS не включается автоматически локальной/dev-командой. Если TLS завершает LB, используйте только base+prod override; LB должен быть единственным доверенным источником `X-Forwarded-Proto: https`; blindly trusting this header from the public port недопустимо. Перед production необходимо проверить реальный LB, `Secure` cookies и trusted proxy IP list.

Nginx limits now apply to both `/v1/*` and the actual Next.js `/api/*` routes. If an external LB is used, configure `real_ip` only for its fixed CIDRs; otherwise `$binary_remote_addr` can collapse all users into one bucket. SSE reading routes `/v1/readings` and the browser-facing `/api/readings` use HTTP/1.1, disabled buffering and bounded timeouts. The CSP allows framing only from the known Telegram Web origin `https://web.telegram.org`; `X-Frame-Options: DENY` is intentionally absent because it would override that allowance. The native Telegram WebView origin was not independently verifiable here and must be checked before broadening the allowlist.

## Релиз и CI

CI pins actions and images by immutable SHA/digest, builds clean API/admin images, syntax-checks the admin UI, validates the migration manifest, mounts migrations read-only, checks that required adminctl/UI/migration files are tracked, and refuses dirty or incomplete schemas before Go E2E tests.

The tag workflow starts only after the `ci` workflow succeeds, validates a semantic `vMAJOR.MINOR.PATCH` tag, fetches that exact ref, checks out a detached commit, uses the production+native-TLS override, runs migration readiness, waits for Compose health, checks the actual public HTTPS `/readyz` body, checks the actual admin loopback `/readyz`, and fails if the admin binding is not loopback. There is no fallback from `/readyz` to `/`.

The release checkout is intentionally detached. A VPS workspace with tracked changes is rejected; keep runtime state outside the repository and do not force-push tags.
