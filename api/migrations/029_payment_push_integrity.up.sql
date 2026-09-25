DROP TRIGGER IF EXISTS payments_snapshot_guard ON payments;
CREATE TRIGGER payments_snapshot_guard
BEFORE INSERT OR UPDATE OF plan_id, plan_code, price_rub_snapshot, amount_rub, stars, purchase_fingerprint, duration_days_snapshot, idempotency_key, status, refund_state
ON payments
FOR EACH ROW
EXECUTE FUNCTION payments_snapshot_guard();

CREATE TABLE payment_webhook_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  payment_id UUID NOT NULL,
  event_hash TEXT NOT NULL CHECK (event_hash ~ '^[0-9a-f]{64}$'),
  reason TEXT NOT NULL,
  currency TEXT NOT NULL,
  total_amount INTEGER NOT NULL CHECK (total_amount > 0),
  telegram_charge_id TEXT NOT NULL,
  provider_charge_id TEXT NOT NULL,
  owner_tg_id BIGINT NOT NULL CHECK (owner_tg_id > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (payment_id, event_hash)
);

CREATE INDEX idx_payment_webhook_events_payment_created
  ON payment_webhook_events (payment_id, created_at DESC);

ALTER TABLE payments VALIDATE CONSTRAINT payments_status_check;
ALTER TABLE payments VALIDATE CONSTRAINT payments_purchase_fingerprint_check;
ALTER TABLE payments VALIDATE CONSTRAINT payments_idempotency_key_check;
ALTER TABLE payments VALIDATE CONSTRAINT payments_amounts_check;
ALTER TABLE payments VALIDATE CONSTRAINT payments_duration_snapshot_check;
ALTER TABLE payments VALIDATE CONSTRAINT payments_refund_state_check;
ALTER TABLE payments VALIDATE CONSTRAINT payments_refund_state_transition_check;
ALTER TABLE subscriptions VALIDATE CONSTRAINT subscriptions_source_type_check;
ALTER TABLE subscriptions VALIDATE CONSTRAINT subscriptions_price_snapshot_check;
