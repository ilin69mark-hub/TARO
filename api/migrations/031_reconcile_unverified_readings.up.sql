CREATE TABLE IF NOT EXISTS reading_quota_repair_031 (
  reading_id UUID PRIMARY KEY REFERENCES readings (id) ON DELETE CASCADE,
  original_status TEXT NOT NULL,
  original_quota_state TEXT NOT NULL,
  original_lease_until TIMESTAMPTZ,
  repaired_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO reading_quota_repair_031
  (reading_id, original_status, original_quota_state, original_lease_until)
SELECT id, status, quota_state, worker_lease_until
  FROM readings
 WHERE status = 'pending'
   AND quota_state = 'unchecked'
   AND (worker_lease_until IS NULL OR worker_lease_until <= now())
   AND updated_at <= now() - interval '2 minutes'
ON CONFLICT (reading_id) DO NOTHING;

UPDATE readings r
   SET status = 'failed', quota_state = 'error',
       worker_claim_token = NULL, worker_lease_until = NULL, updated_at = now()
  FROM reading_quota_repair_031 q
 WHERE q.reading_id = r.id
   AND r.status = q.original_status
   AND r.quota_state = q.original_quota_state
   AND r.worker_lease_until IS NOT DISTINCT FROM q.original_lease_until
   AND (r.worker_lease_until IS NULL OR r.worker_lease_until <= now());
