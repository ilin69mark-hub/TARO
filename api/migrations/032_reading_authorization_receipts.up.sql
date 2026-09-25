CREATE TABLE IF NOT EXISTS reading_authorization_receipts (
  reading_id UUID PRIMARY KEY REFERENCES readings (id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('subscription', 'love_weekly', 'daily', 'single', 'legacy')),
  entitlement_id UUID NULL REFERENCES single_entitlements (id) ON DELETE SET NULL,
  period_start DATE NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (entitlement_id IS NULL OR kind = 'single'),
  CHECK (period_start IS NULL OR kind IN ('daily', 'love_weekly'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_reading_authorization_receipts_entitlement
  ON reading_authorization_receipts (entitlement_id)
  WHERE entitlement_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_reading_authorization_receipts_user_kind
  ON reading_authorization_receipts (user_id, kind, period_start);

CREATE INDEX IF NOT EXISTS idx_readings_authorization_recovery
  ON readings (status, worker_lease_until, updated_at, created_at)
  WHERE quota_state = 'unchecked'
    AND status IN ('pending', 'pending_fallback');

INSERT INTO reading_authorization_receipts
  (reading_id, user_id, kind, entitlement_id, period_start)
SELECT r.id, r.user_id, 'single', e.id, NULL
  FROM readings r
  JOIN single_entitlements e ON e.consumed_reading_id = r.id
ON CONFLICT (reading_id) DO NOTHING;

INSERT INTO reading_authorization_receipts
  (reading_id, user_id, kind, entitlement_id, period_start)
SELECT r.id, r.user_id, 'legacy', NULL, NULL
  FROM readings r
 WHERE r.quota_state = 'allowed'
   AND NOT EXISTS (
     SELECT 1 FROM single_entitlements e WHERE e.consumed_reading_id = r.id
   )
ON CONFLICT (reading_id) DO NOTHING;

CREATE OR REPLACE FUNCTION reading_terminal_quota_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.quota_state <> 'allowed'
     AND (
       NEW.status IN ('done', 'filtered')
       OR (NEW.interpretation IS DISTINCT FROM OLD.interpretation AND COALESCE(NEW.interpretation, '') <> '')
     ) THEN
    RAISE EXCEPTION 'reading result requires allowed quota';
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS readings_terminal_quota_guard ON readings;
CREATE TRIGGER readings_terminal_quota_guard
BEFORE UPDATE ON readings
FOR EACH ROW
EXECUTE FUNCTION reading_terminal_quota_guard();
