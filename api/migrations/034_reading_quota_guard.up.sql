DO $$
BEGIN
  IF to_regclass('public.readings') IS NULL
     OR to_regclass('public.reading_authorization_receipts') IS NULL THEN
    RAISE EXCEPTION '034 requires readings and reading_authorization_receipts';
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
     WHERE table_schema='public' AND table_name='readings' AND column_name='quota_state'
  ) THEN
    RAISE EXCEPTION '034 requires readings.quota_state from migration 028';
  END IF;
END
$$;

CREATE TABLE IF NOT EXISTS reading_quota_quarantine_034 (
  reading_id UUID PRIMARY KEY,
  user_id UUID NOT NULL,
  original_status TEXT NOT NULL,
  original_quota_state TEXT NOT NULL,
  original_interpretation TEXT NOT NULL,
  quarantined_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_reading_quota_quarantine_034_user
  ON reading_quota_quarantine_034 (user_id, quarantined_at DESC);

CREATE OR REPLACE FUNCTION reading_terminal_quota_guard_034()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.quota_state IS DISTINCT FROM 'allowed'
     AND (
       COALESCE(NEW.interpretation, '') <> ''
       OR NEW.status IN ('done', 'filtered')
     ) THEN
    RAISE EXCEPTION 'reading result requires allowed quota';
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS readings_terminal_quota_guard ON readings;
CREATE TRIGGER readings_terminal_quota_guard
BEFORE INSERT OR UPDATE ON readings
FOR EACH ROW
EXECUTE FUNCTION reading_terminal_quota_guard_034();

INSERT INTO reading_quota_quarantine_034
  (reading_id, user_id, original_status, original_quota_state, original_interpretation)
SELECT id, user_id, status, quota_state, COALESCE(interpretation, '')
  FROM readings
 WHERE quota_state IS DISTINCT FROM 'allowed'
   AND (COALESCE(interpretation, '') <> '' OR status IN ('done', 'filtered'))
ON CONFLICT (reading_id) DO NOTHING;

UPDATE readings
   SET interpretation='',
       status='failed',
       quota_state='error',
       worker_claim_token=NULL,
       worker_lease_until=NULL,
       updated_at=now()
 WHERE quota_state IS DISTINCT FROM 'allowed'
   AND (COALESCE(interpretation, '') <> '' OR status IN ('done', 'filtered'));
