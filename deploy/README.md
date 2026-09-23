# Deploy / VPS (T30)

## Первый раз на VPS ($10, Ubuntu)
1. `git clone <repo> /opt/taro`, секреты в окружении хоста (НИЧЕГО в git):
   `POSTGRES_PASSWORD, JWT_SECRET, TG_BOT_TOKEN, TG_STARS_SECRET_TOKEN, OPENROUTER_API_KEY`.
2. UFW (админка 8081 наружу запрещена, см. Книгу 04-architecture/01):
   ```
   ufw allow 22/tcp
   ufw allow 80/tcp
   ufw allow 443/tcp
   ufw deny 8081/tcp
   ufw enable
   ```
   Проверка: `ss -tlnp | grep 8081` — снаружи `curl http://VPS:8081/healthz` → timeout.
3. `docker compose up -d db cache`, `./deploy/migrate.sh up` (DATABASE_URL внутрь сети db).
4. `docker compose build && docker compose up -d`.
5. Cron (см. V22, U25, V23 — все ручки только через SSH-туннель :8081):
   ```
   0 4 * * * /opt/taro/deploy/backup.sh
   5 0 * * * curl -s -X POST http://127.0.0.1:8081/v1/admin/rotate-seasonal -H 'Content-Type: application/json' -d '{}'
   5 21 * * * curl -s -X POST http://127.0.0.1:8081/v1/admin/push-evening -H 'Content-Type: application/json' -d '{}'
   30 21 * * * curl -s -X POST http://127.0.0.1:8081/v1/admin/push-streak-risk -H 'Content-Type: application/json' -d '{}'
   0 9 * * * curl -s -X POST http://127.0.0.1:8081/v1/admin/remind-expiring -H 'Content-Type: application/json' -d '{}'
   ```

## Доступ к админке
Только SSH-туннель: `ssh -L 8081:127.0.0.1:8081 user@VPS`, затем `http://127.0.0.1:8081/v1/admin/config`.

## Релиз
Тег `v*` → GH Actions → скрипт deploy (миграции → build → up → smoke).
Только до 18:00 МСК. Откат: `git checkout <prev-tag>` + повторный прогон (down-миграции — вручную!).
