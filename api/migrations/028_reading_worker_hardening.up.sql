ALTER TABLE readings
  ADD COLUMN IF NOT EXISTS quota_state TEXT NOT NULL DEFAULT 'unchecked';

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
     WHERE conrelid = 'readings'::regclass
       AND conname = 'readings_quota_state_check'
  ) THEN
    ALTER TABLE readings
      ADD CONSTRAINT readings_quota_state_check
      CHECK (quota_state IN ('unchecked', 'allowed', 'denied', 'error'));
  END IF;
END
$$;

UPDATE readings
   SET quota_state = 'allowed'
 WHERE quota_state = 'unchecked'
   AND status IN ('done', 'filtered', 'pending_fallback');

UPDATE readings
   SET quota_state = 'denied'
 WHERE quota_state = 'unchecked'
   AND status = 'cancelled';

UPDATE readings
   SET quota_state = 'error'
 WHERE quota_state = 'unchecked'
   AND status = 'failed';

UPDATE readings
   SET status = 'failed', quota_state = 'error',
       worker_claim_token = NULL, worker_lease_until = NULL, updated_at = now()
 WHERE quota_state = 'unchecked'
   AND status = 'pending'
   AND worker_lease_until IS NULL
   AND updated_at <= now() - interval '2 minutes';

CREATE INDEX IF NOT EXISTS idx_readings_worker_quota
  ON readings (status, quota_state, worker_lease_until, updated_at)
  WHERE status IN ('pending', 'pending_fallback')
    AND quota_state = 'allowed';
