DROP TABLE IF EXISTS ai_budget_ledger;

ALTER TABLE readings
  DROP COLUMN IF EXISTS worker_claim_token,
  DROP COLUMN IF EXISTS worker_lease_until,
  DROP COLUMN IF EXISTS worker_attempts;

ALTER TABLE ai_logs
  DROP COLUMN IF EXISTS provider_usage,
  DROP COLUMN IF EXISTS cost_micros;

DROP INDEX IF EXISTS idx_readings_worker_claim;
