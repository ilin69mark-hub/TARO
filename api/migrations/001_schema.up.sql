-- 001_schema.up.sql — DDL пакета A (см. docs/project-book/04-architecture/03-database-schema.md).
-- Порядок: независимые → зависимые. Все деньги/статусы — через CHECK, версии цен — UNIQUE(code, valid_from).

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- users: TG или anon, partial UNIQUE (T03 audit-fix A)
CREATE TABLE users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tg_id BIGINT NULL,
  anon_uuid UUID NULL,
  fingerprint TEXT NULL,
  role TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
  age_confirmed_at TIMESTAMPTZ NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_users_tg ON users (tg_id) WHERE tg_id IS NOT NULL;
CREATE UNIQUE INDEX idx_users_anon ON users (anon_uuid) WHERE anon_uuid IS NOT NULL;

-- plans: daily_limit убран (живет в app_config.free.daily_limit); версии цен — UNIQUE(code, valid_from)
CREATE TABLE plans (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code TEXT NOT NULL CHECK (code IN ('free', 'month_299', 'year_2490', 'single_99')),
  price_rub INT NOT NULL CHECK (price_rub >= 0),
  stars_amount INT NOT NULL CHECK (stars_amount >= 0),
  duration_days INT NULL CHECK (duration_days IS NULL OR duration_days > 0),
  is_active BOOL NOT NULL DEFAULT true,
  valid_from TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (code, valid_from)
);
CREATE INDEX idx_plans_active ON plans (is_active) WHERE is_active;

-- spreads: реестр раскладов, удаление запрещено (RESTRICT со стороны readings)
CREATE TABLE spreads (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code TEXT NOT NULL UNIQUE CHECK (code IN ('daily', 'three', 'love', 'decision', 'celtic')),
  name_ru TEXT NOT NULL,
  positions JSONB NOT NULL DEFAULT '[]',
  is_premium BOOL NOT NULL DEFAULT false,
  is_active BOOL NOT NULL DEFAULT true,
  sort_order INT NOT NULL DEFAULT 0,
  config_json JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_spreads_active_sort ON spreads (is_active, sort_order) WHERE is_active;

-- cards: 78 арканов, только SELECT в рантайме (seed — T04)
CREATE TABLE cards (
  id SMALLINT PRIMARY KEY CHECK (id BETWEEN 0 AND 77),
  name_ru TEXT NOT NULL,
  arcana TEXT NOT NULL CHECK (arcana IN ('major', 'minor')),
  suit TEXT NULL CHECK (suit IS NULL OR suit IN ('wands', 'cups', 'swords', 'pentacles')),
  upright_ru TEXT NOT NULL,
  reversed_ru TEXT NOT NULL,
  image_key TEXT NOT NULL
);

-- readings: идемпотентность — UNIQUE(user_id, idempotency_key) (не глобальный!)
CREATE TABLE readings (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  spread_code TEXT NOT NULL REFERENCES spreads (code) ON DELETE RESTRICT,
  question TEXT NULL CHECK (question IS NULL OR char_length(question) <= 500),
  cards JSONB NOT NULL DEFAULT '[]',
  interpretation TEXT NULL,
  seed BIGINT NOT NULL,
  algo_version SMALLINT NOT NULL DEFAULT 1,
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'done', 'pending_fallback', 'failed', 'filtered')),
  idempotency_key TEXT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, idempotency_key)
);
CREATE INDEX idx_readings_user_created ON readings (user_id, created_at DESC);

-- subscriptions: ЕДИНСТВЕННЫЙ источник valid_until (trial/referral пишут сюда bonus-планами)
CREATE TABLE subscriptions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  plan_id UUID NOT NULL REFERENCES plans (id),
  plan_code TEXT NOT NULL,
  price_rub_snapshot INT NOT NULL,
  valid_until TIMESTAMPTZ NOT NULL,
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'expired', 'revoked')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_sub_user_valid ON subscriptions (user_id, valid_until DESC);

-- payments: снапшот цены + Stars-идемпотентность через provider_payment_id
CREATE TABLE payments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  plan_id UUID NOT NULL REFERENCES plans (id),
  plan_code TEXT NOT NULL, -- денормализация кода на момент покупки (FK нет: у plans UNIQUE(code, valid_from), не UNIQUE(code)); истина — plan_id + price_rub_snapshot
  price_rub_snapshot INT NOT NULL,
  provider TEXT NOT NULL DEFAULT 'tg_stars' CHECK (provider IN ('tg_stars')),
  provider_payment_id TEXT NOT NULL UNIQUE,
  amount_rub INT NOT NULL,
  stars INT NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'succeeded', 'refunded', 'expired')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_pay_provider ON payments (provider_payment_id);
CREATE INDEX idx_pay_status_created ON payments (status, created_at);

-- single_entitlements: разовая покупка 99₽ привязана к (user, spread, payment) — безлимита нет
CREATE TABLE single_entitlements (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  spread_code TEXT NOT NULL REFERENCES spreads (code) ON DELETE RESTRICT,
  payment_id UUID NOT NULL UNIQUE REFERENCES payments (id),
  consumed_reading_id UUID NULL UNIQUE REFERENCES readings (id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, payment_id)
);

-- entitlements: только счетчики free/love/referral-капов (valid_until и trial живут в subscriptions!)
CREATE TABLE entitlements (
  user_id UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  free_used_today INT NOT NULL DEFAULT 0,
  free_date DATE NULL,
  love_used_week INT NOT NULL DEFAULT 0,
  love_week DATE NULL,
  referral_bonus_month INT NOT NULL DEFAULT 0,
  referral_bonus_month_key TEXT NULL,
  referral_bonus_lifetime INT NOT NULL DEFAULT 0
);

-- referrals: код UNIQUE, completed только при referee.tg_id NOT NULL (антиферма)
CREATE TABLE referrals (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  referrer_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  referee_id UUID NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
  code TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed', 'rejected')),
  bonus_days INT NOT NULL DEFAULT 3,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (referrer_id != referee_id)
);

-- ai_logs: аудит стоимости и breaker
CREATE TABLE ai_logs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  reading_id UUID NULL REFERENCES readings (id) ON DELETE SET NULL,
  model TEXT NOT NULL,
  prompt_hash TEXT NOT NULL,
  tokens_in INT NOT NULL DEFAULT 0,
  tokens_out INT NOT NULL DEFAULT 0,
  latency_ms INT NOT NULL DEFAULT 0,
  status TEXT NOT NULL CHECK (status IN ('ok', 'failed', 'filtered', 'breaker')),
  error TEXT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_ai_reading ON ai_logs (reading_id);
CREATE INDEX idx_ai_status_created ON ai_logs (status, created_at);

-- app_config: все настраиваемое из админки (см. 02-functional/07)
CREATE TABLE app_config (
  key TEXT PRIMARY KEY,
  value JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- admin_audit: кто что поменял (admin_id → users с role=admin)
CREATE TABLE admin_audit (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  admin_id UUID NOT NULL REFERENCES users (id),
  action TEXT NOT NULL,
  diff JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_admin_audit_admin ON admin_audit (admin_id, created_at DESC);
