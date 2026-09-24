# Deploy / VPS

## Конфигурация

`docker-compose.yml` — локальный/dev-контур. В нём оставлены только dev-значения для удобства `.env.example`; production workflow никогда не запускает этот файл без `deploy/docker-compose.prod.yml`.

Для production override используется Docker Compose v2.24+ (поддерживает `!reset`/`!override`).

Production-команда:

```sh
COMPOSE=(docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml)
"${COMPOSE[@]}" config --quiet
```

Production override требует настоящие значения и не содержит dev-секретов. В `.env` на VPS задаются как минимум:

- `DATABASE_URL` с хостом `db` внутри Compose, либо `%`-encoded URL для внешней БД;
- `POSTGRES_PASSWORD`, `REDIS_PASSWORD`, `REDIS_ENC_KEY` (64 hex-символа);
- `OPENROUTER_API_KEY`, `OPENROUTER_API_KEY_2`;
- `JWT_SECRET`, `TG_BOT_TOKEN`, `TG_STARS_SECRET_TOKEN`;
- `VAPID_PUBLIC_KEY`, `VAPID_PRIVATE_KEY`;
- `ADMIN_TG_IDS`, `ADMIN_API_TOKEN`;
- `BACKUP_GPG_KEY` для production backup;
- `NEXT_PUBLIC_BASE_URL`.

`DATABASE_URL` не должен указывать на `localhost` внутри контейнеров. Если пароль содержит специальные символы, используйте URL-encoding. Файл `.env` должен иметь режим `600`; не помещайте его в git или backup-архив.

При ротации VAPID меняйте пару `VAPID_PUBLIC_KEY`/`VAPID_PRIVATE_KEY` одновременно, пересоздавайте API-контейнеры и просите клиентов повторно opt-in push; старые подписки нельзя считать валидными после смены applicationServerKey.

Локальная разработка использует текущий `.env.example` с `POSTGRES_PASSWORD`: Compose и `migrate.sh` сами строят URL для `db`. Явный `DATABASE_URL` можно добавить в локальный `.env`, если нужна другая БД.

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

`migrate.sh` принимает `up`, `verify` и ручной `down [steps]`. `down` дополнительно требует `ALLOW_DOWN=1` и подтверждение `YES`. Перед backup и любым down сначала сохраните дамп.

Скрипт монтирует SQL только в режиме `:ro`, запускает migration-контейнер с read-only filesystem, без capabilities и с `no-new-privileges`. После `up` он проверяет через `schema-ready.sh`:

- наличие `schema_migrations`;
- `dirty=false`;
- точное совпадение с последней версией из `api/migrations`;
- наличие основных и поздних таблиц, включая `trial_grants` и `share_tokens`.

При `DATABASE_URL_FILE` файл содержит одну строку с URL. Скрипт создаёт случайный временный env-файл с правами `600`, удаляет его через trap и не передаёт URL в аргументах `docker compose`.

### Восстановление legacy dirty-схемы

Если `schema_migrations` содержит ровно `version=1, dirty=true`, а таблицы 001–016 уже существуют, обычный `migrate.sh up` блокируется намеренно. Для этой подтверждённой legacy-формы есть отдельная процедура:

```sh
ALLOW_REPAIR=1 BACKUP_FILE=/secure/path/taro-before-repair.dump ./deploy/recover-dirty.sh
```

Процедура сначала делает приватный dump, применяет только миграции 017–026, фиксирует version 26 и запускает `schema-check`. Если обнаружена любая другая схема, скрипт отказывается менять её; нужно сначала провести ручной schema diff. После repair перезапустите API и проверьте `schema_migrations`.

## Сети и admin

`web` подключён только к `edge` и не имеет доступа к `db` или `cache`. В dev БД и Redis доступны хосту только через `127.0.0.1`; отдельная `dev-access` сеть не выдаётся web-контейнеру. Production override убирает host-порты БД и Redis. Старая сеть `internal` сохраняет совместимость при upgrade и не меняет membership-check; полная изоляция web обеспечивается именно отсутствием web в этой сети.

`api-admin` слушает `127.0.0.1:8081` внутри контейнера. `admin-access` использует тот же network namespace, слушает `8082`, а host-порт проксирует только на `127.0.0.1:${ADMIN_HOST_PORT:-8081}`. Поэтому работают без публичного доступа:

```sh
curl --fail http://127.0.0.1:8081/healthz
ssh -L 8081:127.0.0.1:8081 user@VPS
```

После открытия туннеля admin доступен на `http://127.0.0.1:8081`. Проверка публичности:

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

Если TLS завершает сам nginx, используется opt-in override с каталогом `fullchain.pem` и `privkey.pem`:

```sh
TLS_CERT_DIR=/etc/letsencrypt/live/example.com \
  docker compose -f docker-compose.yml -f deploy/docker-compose.prod.yml -f deploy/docker-compose.tls.yml up -d
```

`deploy/nginx-tls.conf` включает TLS 1.2/1.3, HSTS и безопасные proxy headers. Сертификаты неизвестны этому репозиторию, поэтому TLS-файл не включается автоматически. Если TLS завершает LB, LB должен быть единственным доверенным источником `X-Forwarded-Proto: https`; blindly trusting this header from the public port недопустимо. Перед production необходимо проверить реальный LB, `Secure` cookies и trusted proxy IP list.

Nginx limits now apply to both `/v1/*` and the actual Next.js `/api/*` routes. If an external LB is used, configure `real_ip` only for its fixed CIDRs; otherwise `$binary_remote_addr` can collapse all users into one bucket. SSE reading routes `/v1/readings` and the browser-facing `/api/readings` use HTTP/1.1, disabled buffering and bounded timeouts. The CSP allows framing only from the known Telegram Web origin `https://web.telegram.org`; `X-Frame-Options: DENY` is intentionally absent because it would override that allowance. The native Telegram WebView origin was not independently verifiable here and must be checked before broadening the allowlist.

## Релиз и CI

CI pins actions and images by immutable SHA/digest, mounts migrations read-only, and refuses dirty or incomplete schemas before Go E2E tests.

The tag workflow validates a semantic `vMAJOR.MINOR.PATCH` tag, fetches that exact ref, checks out a detached commit, uses the production override, runs migration readiness, waits for Compose health, checks the actual public `/healthz` body, checks the actual admin loopback health, and fails if the admin binding is not loopback. There is no fallback from `/healthz` to `/`.

The release checkout is intentionally detached. A VPS workspace with tracked changes is rejected; keep runtime state outside the repository and do not force-push tags.
