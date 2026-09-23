# C4: Context → Containers → Components (текстом)

> Статус: `frozen v1.0 (2026-09-23)`. Связи: `02-interaction-next-go.md`, `03-database-schema.md`.

## Level 1 — Context
Акторы: User (mobile PWA / TG WebApp), Admin (ты), Внешние: Telegram API (auth, Stars), OpenRouter (LLM).
Система «Онлайн Таро» — единственный контур доверия для денег и данных. Админ-контур изолирован.

## Level 2 — Containers
| Контейнер | Технология | Ответственность | Порт / сеть |
|---|---|---|---|
| `web` | Next.js 14 App Router, TS, Tailwind, FM | UI, PWA, SSE-клиент, `/admin` UI | 3000 (public via nginx) |
| `api-public` | Go 1.25, chi, pgx v5 + go-redis v9 (1.22 невозможен: свежие pgx/redis требуют ≥1.23) | auth, entitlements, readings, payments | 8080 (public via nginx) |
| `api-admin` | Go 1.25, тот же бинарь, флаг `--admin` | admin config/publish, audit | 8081 (`127.0.0.1` only, SSH-туннель) |
| `db` | PostgreSQL 16 | users→readings, plans, spreads | 5432 (internal) |
| `cache` | Redis 7 | сессии, лимиты, кэш spreads/AI | 6379 (internal) |
| `worker` | Go горутина в api (позже отдельный) | дописывание AI при таймауте, nightly sync лимитов | — |

Все за `nginx` (TLS, rate limit L7). Docker Compose на VPS $10. `nginx: deny 8081 public (listen только 127.0.0.1:8081), allow 127.0.0.1; UFW deny 8081/tcp извне`. Секреты: `OPENROUTER_API_KEY{,_2}`, `TG_BOT_TOKEN`, `TG_STARS_SECRET_TOKEN`, `JWT_SECRET` — только env VPS, `.env` в git запрещен (CI fail).

## Level 3 — Components (api)
`auth`, `entitlements`, `spreads`, `readings`, `ai-gateway`, `payments`, `admin`, `antifraud-mw`.
Каждый — пакет `internal/<name>` с handler → service → repo. Общее: `pkg/jwt`, `pkg/tgverify` (HMAC initData), `pkg/starsverify` (Secret-Token + IP TG), `pkg/openrouter`.
