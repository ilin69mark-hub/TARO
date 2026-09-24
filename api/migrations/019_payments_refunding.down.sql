DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM payments WHERE status IN ('refunding','reconciliation')) THEN
    RAISE EXCEPTION '019 down refused: reconcile payment states before rollback';
  END IF;
END
$$;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_status_check;
ALTER TABLE payments ADD CONSTRAINT payments_status_check
  CHECK (status IN ('pending','succeeded','refunded','expired'));
