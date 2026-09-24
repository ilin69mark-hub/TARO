DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM payments) THEN
    RAISE EXCEPTION '024 down refused: payment history is not reversible';
  END IF;
END
$$;

DROP TRIGGER IF EXISTS payments_snapshot_guard ON payments;
DROP FUNCTION IF EXISTS payments_snapshot_guard();

DROP TABLE IF EXISTS payment_refunds;
DROP INDEX IF EXISTS idx_pay_telegram_charge;
DROP INDEX IF EXISTS idx_pay_provider_charge;
DROP INDEX IF EXISTS idx_pay_reconciliation;
DROP INDEX IF EXISTS idx_pay_refund_state;
DROP INDEX IF EXISTS idx_sub_payment_source;

ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_source_type_check;
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_price_snapshot_check;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS source_type;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS payment_id;

ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_status_check;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_purchase_fingerprint_check;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_idempotency_key_check;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_amounts_check;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_duration_snapshot_check;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_refund_state_check;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_refund_state_transition_check;
ALTER TABLE payments
  DROP COLUMN IF EXISTS reconciliation_reason,
  DROP COLUMN IF EXISTS refund_last_error,
  DROP COLUMN IF EXISTS refund_confirmed_at,
  DROP COLUMN IF EXISTS refund_attempted_at,
  DROP COLUMN IF EXISTS refund_requested_at,
  DROP COLUMN IF EXISTS refund_state,
  DROP COLUMN IF EXISTS paid_at,
  DROP COLUMN IF EXISTS provider_verified_at,
  DROP COLUMN IF EXISTS provider_payment_charge_id,
  DROP COLUMN IF EXISTS telegram_payment_charge_id,
  DROP COLUMN IF EXISTS duration_days_snapshot,
  DROP COLUMN IF EXISTS purchase_fingerprint;

ALTER TABLE payments ADD CONSTRAINT payments_status_check
  CHECK (status IN ('pending','succeeded','refunded','expired','refunding'));
