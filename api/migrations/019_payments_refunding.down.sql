-- 019 down: сначала сводим зависшие refunding в succeeded (иначе CHECK-violation + dirty).
-- ВНИМАНИЕ: refunding означает «TG-вызов неизвестного исхода» — сверь с TG перед откатом.
UPDATE payments SET status = 'succeeded' WHERE status = 'refunding';
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_status_check;
ALTER TABLE payments ADD CONSTRAINT payments_status_check
  CHECK (status IN ('pending','succeeded','refunded','expired'));
