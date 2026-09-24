-- 018 down: сначала сводим cancelled в failed (иначе CHECK-violation + dirty).
UPDATE readings SET status = 'failed' WHERE status = 'cancelled';
ALTER TABLE readings DROP CONSTRAINT IF EXISTS readings_status_check;
ALTER TABLE readings ADD CONSTRAINT readings_status_check
  CHECK (status IN ('pending','done','pending_fallback','failed','filtered'));
