# Масштабируемость 100 / 1000 / 10000

> Статус: `draft`. Связи: `04-architecture/01-c4-context-containers.md`.

| Юзеры (DAU) | Что держит VPS $10 (2vCPU/4GB) | Что делаем |
|---|---|---|
| 100 | всё на 1 ноде, PG+Redis+api+web в Docker | ничего, бэкап PG daily |
| 1000 (~100 чтений/вечер) | PG ~50 conn, Redis hit 90%, OpenRouter ~$5/день | выносим worker, `pgbouncer`, AI-кэш, CDN для карт (Cloudflare free) |
| 10000 | 1 нода не держит пик 20:00 | `api-public` ×2 (stateless!) + LB nginx, `api-admin` отдельно `replicas:1` (не скейлить, mTLS/IP-allowlist внутри VPC), PG read-replica или managed, очередь NATS для AI, лимит SSE 200 concurrent |

Stateless-принцип с дня 1: сессии в Redis, файлы карт в `/public` + CDN, никаких локальных стейтов. Миграция на 2 ноды = `docker compose --scale api-public=2` (admin всегда 1) + LB nginx. Админ никогда не за LB публички.
