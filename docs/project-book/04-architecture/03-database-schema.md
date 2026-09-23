# Схема БД (PostgreSQL 16)

> Статус: `audit-fix A (2026-09-23)`. Связи: `02-functional/07-admin-config.md`, `04-api-spec.md`.

## Таблицы
| Таблица | Поля (тип) | Индексы/Связи |
|---|---|---|
| `users` | `id uuid PK, tg_id bigint null, anon_uuid uuid null, referral_code text unique null, fingerprint text, role text, age_confirmed_at, created_at` (коды в колонке — self-строки referrals удалены миграцией 014: они блокировали apply через UNIQUE(referee_id)) | partial UNIQUE tg/anon/refcode |
| `plans` | `id uuid PK, code text (free/month_299/year_2490/single_99), price_rub int, stars_amount int NOT NULL, duration_days int null, is_active bool, valid_from timestamptz` | `UNIQUE(code, valid_from)`, `idx_plans_active` — daily_limit убран (живет только в `app_config.free.daily_limit`) |
| `spreads` | `id uuid PK, code text unique (daily/three/love/decision/celtic), name_ru text, positions jsonb, is_premium bool, is_active bool, sort_order int, config_json jsonb` | `idx_spreads_active_sort`, `FOREIGN KEY(spread_code) REFERENCES spreads(code) ON DELETE RESTRICT` со стороны readings |
| `cards` | `id smallint PK (0–77), name_ru text, arcana text, suit text null, upright_ru text, reversed_ru text, image_key text` | seed-данные, только SELECT |
| `readings` | `id uuid PK, user_id FK users ON DELETE CASCADE, spread_code text FK spreads(code) ON DELETE RESTRICT, question text, cards jsonb, interpretation text, seed bigint, algo_version smallint default 1 (Fisher–Yates + Bernoulli(0.15) v1), status text CHECK(status IN ('pending','done','pending_fallback','failed','filtered')) DEFAULT 'pending', idempotency_key text, created_at, updated_at timestamptz` | `UNIQUE(user_id, idempotency_key)`, `idx_readings_user_created` |
| `subscriptions` | `id uuid PK, user_id FK ON DELETE CASCADE, plan_id FK plans(id), plan_code text NOT NULL, price_rub_snapshot int NOT NULL, valid_until timestamptz, status text CHECK(status IN ('active','expired','revoked')) DEFAULT 'active', created_at` | `idx_sub_user_valid`, единственный источник `valid_until` (trial/referral пишут сюда же как bonus-планы) |
| `payments` | `id uuid PK, user_id FK ON DELETE CASCADE, plan_id FK plans(id), plan_code text NOT NULL (без FK: у plans UNIQUE(code,valid_from)), price_rub_snapshot int NOT NULL, provider text, provider_payment_id text unique, amount_rub int, stars int, status text CHECK, created_at` | `idx_pay_provider`, `idx_pay_status_created(status,created_at)` для job `expire pending>15м` |
| `single_entitlements` | `id uuid PK, user_id FK ON DELETE CASCADE, spread_code text NOT NULL, payment_id FK payments(id) UNIQUE, consumed_reading_id FK readings(id) null, created_at` | `UNIQUE(user_id, payment_id)` — разовая покупка 99₽ привязана к спреду+платежу, безлимита нет |
| `entitlements` | `user_id PK FK ON DELETE CASCADE, free_used_today int DEFAULT 0, free_date date, love_used_week int DEFAULT 0, love_week date, referral_bonus_month int DEFAULT 0, referral_bonus_month_key text, referral_bonus_lifetime int DEFAULT 0` | `trial_used` и `valid_until` удалены (trial/referral → `subscriptions`); ночные счетчики только для free/love/referral-капов |
| `referrals` | `id uuid PK, referrer_id FK, referee_id FK unique, code text UNIQUE NOT NULL, status text CHECK(pending/completed/rejected), bonus_days int, created_at` (без self-строк с миграции 014; код юзера — users.referral_code) | `UNIQUE(code)`, `UNIQUE(referee_id)`, генерация кода при первом `GET /referral/me` |
| `ai_logs` | `id uuid PK, reading_id FK, model text, prompt_hash text, tokens_in/out int, latency_ms int, status text CHECK(status IN ('ok','failed','filtered','breaker')) , error text, created_at` | `idx_ai_reading`, `idx_ai_status_created` |
| `app_config` | `key text PK, value jsonb, updated_at` | ключи: `free.daily_limit=1`, `love.free_weekly=1`, `history.free_limit=20`, `trial.{enabled:true,days:3,require_tg:true}`, `referral.{bonus_days:3,monthly_cap:30}`, `copy.paywall_*`, `ai.*` |
| `admin_audit` | `id uuid PK, admin_id UUID REFERENCES users(id), action text, diff jsonb, created_at` | `CHECK` роли через `users.role='admin'` + seed admin |

## Миграции
`golang-migrate`, `migrations/NNN_*.sql`, seed `cards` + `spreads` + `plans` дефолтными. Удаление spreads запрещено FK `RESTRICT`.
