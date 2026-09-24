CREATE TABLE IF NOT EXISTS ai_budget_ledger (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  month_start DATE NOT NULL,
  reading_id UUID NULL REFERENCES readings (id) ON DELETE SET NULL,
  model TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('reserved', 'consumed', 'released')),
  tokens_in INTEGER NOT NULL DEFAULT 0 CHECK (tokens_in >= 0),
  tokens_out INTEGER NOT NULL DEFAULT 0 CHECK (tokens_out >= 0),
  provider_usage JSONB NULL,
  cost_micros BIGINT NOT NULL DEFAULT 0 CHECK (cost_micros >= 0),
  reserved_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS idx_ai_budget_month_status
  ON ai_budget_ledger (month_start, status);

ALTER TABLE ai_logs
  ADD COLUMN IF NOT EXISTS provider_usage JSONB,
  ADD COLUMN IF NOT EXISTS cost_micros BIGINT NOT NULL DEFAULT 0;

ALTER TABLE readings
  ADD COLUMN IF NOT EXISTS worker_claim_token UUID,
  ADD COLUMN IF NOT EXISTS worker_lease_until TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS worker_attempts INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_readings_worker_claim
  ON readings (status, worker_lease_until, updated_at)
  WHERE status IN ('pending', 'pending_fallback');

INSERT INTO ai_budget_ledger
  (month_start, reading_id, model, status, tokens_in, tokens_out, provider_usage, cost_micros, reserved_at, completed_at)
SELECT date_trunc('month', created_at)::date,
       reading_id,
       model,
       'consumed',
       tokens_in,
       tokens_out,
       NULL,
       cost_micros,
       created_at,
       created_at
  FROM ai_logs
 WHERE status = 'ok'
   AND (error IS NULL OR error <> 'cache_hit')
   AND created_at >= date_trunc('month', now());
