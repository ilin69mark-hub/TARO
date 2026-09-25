LOCK TABLE payments, subscriptions, payment_webhook_events IN ACCESS EXCLUSIVE MODE;

DO $$
BEGIN
  IF current_setting('taro.migration_029_preflight', true) IS DISTINCT FROM 'confirmed' THEN
    RAISE EXCEPTION '029 down refused: 029 is not generally reversible; runner preflight required after backup and checking later migrations, set taro.migration_029_preflight=confirmed';
  END IF;
  IF EXISTS (SELECT 1 FROM payments)
     OR EXISTS (SELECT 1 FROM subscriptions)
     OR EXISTS (SELECT 1 FROM payment_webhook_events) THEN
    RAISE EXCEPTION '029 down refused: payment history and webhook audit are not reversible';
  END IF;
END
$$;

DROP TABLE IF EXISTS payment_webhook_events;

DROP TRIGGER IF EXISTS payments_snapshot_guard ON payments;
CREATE TRIGGER payments_snapshot_guard
BEFORE INSERT OR UPDATE OF plan_code, price_rub_snapshot, stars, purchase_fingerprint, duration_days_snapshot, status, refund_state
ON payments
FOR EACH ROW
EXECUTE FUNCTION payments_snapshot_guard();

ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_idempotency_key_check;
ALTER TABLE payments ADD CONSTRAINT payments_idempotency_key_check
  CHECK (idempotency_key IS NULL OR char_length(idempotency_key) BETWEEN 1 AND 64) NOT VALID;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_amounts_check;
ALTER TABLE payments ADD CONSTRAINT payments_amounts_check
  CHECK (price_rub_snapshot >= 0 AND amount_rub >= 0 AND stars >= 0) NOT VALID;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_duration_snapshot_check;
ALTER TABLE payments ADD CONSTRAINT payments_duration_snapshot_check
  CHECK (duration_days_snapshot IS NULL OR duration_days_snapshot > 0) NOT VALID;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_refund_state_transition_check;
ALTER TABLE payments ADD CONSTRAINT payments_refund_state_transition_check
  CHECK (
    (status = 'refunding' AND refund_state IN ('requested','submitted','confirmed','unknown','manual'))
    OR (status = 'refunded' AND refund_state IN ('confirmed','manual'))
    OR (status NOT IN ('refunding','refunded') AND refund_state = 'none')
  ) NOT VALID;
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_price_snapshot_check;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_price_snapshot_check
  CHECK (price_rub_snapshot >= 0) NOT VALID;
