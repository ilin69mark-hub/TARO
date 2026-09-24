ALTER TABLE payments
  ADD COLUMN purchase_fingerprint TEXT,
  ADD COLUMN duration_days_snapshot INTEGER,
  ADD COLUMN telegram_payment_charge_id TEXT,
  ADD COLUMN provider_payment_charge_id TEXT,
  ADD COLUMN provider_verified_at TIMESTAMPTZ,
  ADD COLUMN paid_at TIMESTAMPTZ,
  ADD COLUMN refund_state TEXT NOT NULL DEFAULT 'none',
  ADD COLUMN refund_requested_at TIMESTAMPTZ,
  ADD COLUMN refund_attempted_at TIMESTAMPTZ,
  ADD COLUMN refund_confirmed_at TIMESTAMPTZ,
  ADD COLUMN refund_last_error TEXT,
  ADD COLUMN reconciliation_reason TEXT;

UPDATE payments
   SET purchase_fingerprint = encode(digest(plan_code || '|' || price_rub_snapshot::text || '|' || stars::text, 'sha256'), 'hex')
 WHERE purchase_fingerprint IS NULL;

UPDATE payments p
   SET duration_days_snapshot = pl.duration_days
  FROM plans pl
 WHERE pl.id = p.plan_id
   AND p.duration_days_snapshot IS NULL;

UPDATE payments
   SET refund_state = CASE WHEN status = 'refunded' THEN 'confirmed' ELSE 'unknown' END,
       refund_requested_at = CASE WHEN status IN ('refunded','refunding') THEN COALESCE(refund_requested_at, created_at) ELSE refund_requested_at END
 WHERE status IN ('refunded','refunding');

ALTER TABLE payments ALTER COLUMN purchase_fingerprint SET NOT NULL;

ALTER TABLE subscriptions
  ADD COLUMN source_type TEXT NOT NULL DEFAULT 'legacy',
  ADD COLUMN payment_id UUID NULL REFERENCES payments (id) ON DELETE SET NULL;

CREATE OR REPLACE FUNCTION payments_snapshot_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  expected_fingerprint TEXT;
  plan_duration INTEGER;
BEGIN
  IF TG_OP = 'UPDATE' AND NEW.status = 'refunding' AND OLD.status NOT IN ('succeeded','refunding') THEN
    RAISE EXCEPTION 'refund requires succeeded payment';
  END IF;
  IF TG_OP = 'UPDATE' AND OLD.status = 'succeeded' AND NEW.status = 'refunding' AND NEW.refund_state <> 'requested' THEN
    RAISE EXCEPTION 'refund requires requested state';
  END IF;
  IF TG_OP = 'UPDATE' AND NEW.status = 'refunded' AND OLD.status NOT IN ('refunding','refunded') THEN
    RAISE EXCEPTION 'refund finalization requires refunding payment';
  END IF;
  IF TG_OP = 'UPDATE' AND OLD.status = 'refunding' AND NEW.status = 'refunded' AND NEW.refund_state NOT IN ('confirmed','manual') THEN
    RAISE EXCEPTION 'refund finalization requires confirmed state';
  END IF;
  IF TG_OP = 'UPDATE' AND OLD.status = 'refunding' AND NEW.status = 'succeeded' THEN
    RAISE EXCEPTION 'refunding payment cannot become succeeded';
  END IF;
  IF TG_OP = 'UPDATE' AND (
    NEW.plan_id IS DISTINCT FROM OLD.plan_id OR
    NEW.plan_code IS DISTINCT FROM OLD.plan_code OR
    NEW.price_rub_snapshot IS DISTINCT FROM OLD.price_rub_snapshot OR
    NEW.amount_rub IS DISTINCT FROM OLD.amount_rub OR
    NEW.stars IS DISTINCT FROM OLD.stars OR
    NEW.duration_days_snapshot IS DISTINCT FROM OLD.duration_days_snapshot OR
    NEW.purchase_fingerprint IS DISTINCT FROM OLD.purchase_fingerprint OR
    NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
  ) THEN
    RAISE EXCEPTION 'payment purchase snapshot is immutable';
  END IF;
  expected_fingerprint := encode(digest(NEW.plan_code || '|' || NEW.price_rub_snapshot::text || '|' || NEW.stars::text, 'sha256'), 'hex');
  IF NEW.purchase_fingerprint IS NULL OR btrim(NEW.purchase_fingerprint) = '' THEN
    NEW.purchase_fingerprint := expected_fingerprint;
  ELSIF NEW.purchase_fingerprint <> expected_fingerprint THEN
    RAISE EXCEPTION 'purchase_fingerprint does not match payment snapshot';
  END IF;
  IF NEW.duration_days_snapshot IS NULL THEN
    SELECT duration_days INTO plan_duration FROM plans WHERE id = NEW.plan_id;
    NEW.duration_days_snapshot := plan_duration;
  END IF;
  IF NEW.status = 'refunding' AND NEW.refund_state = 'none' THEN
    NEW.refund_state := 'unknown';
  END IF;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS payments_snapshot_guard ON payments;
CREATE TRIGGER payments_snapshot_guard
BEFORE INSERT OR UPDATE OF plan_code, price_rub_snapshot, stars, purchase_fingerprint, duration_days_snapshot, status, refund_state
ON payments
FOR EACH ROW
EXECUTE FUNCTION payments_snapshot_guard();

DO $$
DECLARE c text;
BEGIN
  SELECT conname INTO c
    FROM pg_constraint
   WHERE conrelid = 'payments'::regclass
     AND contype = 'c'
     AND pg_get_constraintdef(oid) LIKE '%refunding%';
  IF c IS NOT NULL THEN
    EXECUTE format('ALTER TABLE payments DROP CONSTRAINT %I', c);
  END IF;
END
$$;
ALTER TABLE payments ADD CONSTRAINT payments_status_check
  CHECK (status IN ('pending','succeeded','refunded','expired','refunding','reconciliation'));

ALTER TABLE payments
  ADD CONSTRAINT payments_purchase_fingerprint_check
    CHECK (purchase_fingerprint ~ '^[0-9a-f]{64}$'),
  ADD CONSTRAINT payments_idempotency_key_check
    CHECK (idempotency_key IS NULL OR char_length(idempotency_key) BETWEEN 1 AND 64) NOT VALID,
  ADD CONSTRAINT payments_amounts_check
    CHECK (price_rub_snapshot >= 0 AND amount_rub >= 0 AND stars >= 0) NOT VALID,
  ADD CONSTRAINT payments_duration_snapshot_check
    CHECK (duration_days_snapshot IS NULL OR duration_days_snapshot > 0) NOT VALID,
  ADD CONSTRAINT payments_refund_state_check
    CHECK (refund_state IN ('none','requested','submitted','confirmed','unknown','manual')),
  ADD CONSTRAINT payments_refund_state_transition_check
    CHECK (
      (status = 'refunding' AND refund_state IN ('requested','submitted','confirmed','unknown','manual'))
      OR (status = 'refunded' AND refund_state IN ('confirmed','manual'))
      OR (status NOT IN ('refunding','refunded') AND refund_state = 'none')
    ) NOT VALID;

ALTER TABLE subscriptions
  ADD CONSTRAINT subscriptions_source_type_check
    CHECK (source_type IN ('legacy','payment')),
  ADD CONSTRAINT subscriptions_price_snapshot_check
    CHECK (price_rub_snapshot >= 0) NOT VALID;

CREATE TABLE payment_refunds (
  payment_id UUID PRIMARY KEY REFERENCES payments (id) ON DELETE CASCADE,
  state TEXT NOT NULL CHECK (state IN ('requested','submitted','confirmed','unknown','manual')),
  charge_id TEXT NULL,
  requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  submitted_at TIMESTAMPTZ NULL,
  confirmed_at TIMESTAMPTZ NULL,
  last_error TEXT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO payment_refunds (payment_id, state, charge_id, requested_at, confirmed_at, updated_at)
SELECT id,
       CASE WHEN status = 'refunded' THEN 'confirmed' ELSE 'unknown' END,
       NULLIF(split_part(provider_payment_id, ':', 2), ''),
       COALESCE(refund_requested_at, created_at),
       CASE WHEN status = 'refunded' THEN COALESCE(refund_confirmed_at, created_at) ELSE NULL END,
       now()
  FROM payments
 WHERE status IN ('refunded','refunding')
ON CONFLICT (payment_id) DO NOTHING;

CREATE UNIQUE INDEX IF NOT EXISTS idx_pay_telegram_charge
  ON payments (provider, telegram_payment_charge_id)
  WHERE provider = 'tg_stars' AND telegram_payment_charge_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_pay_provider_charge
  ON payments (provider, provider_payment_charge_id)
  WHERE provider = 'tg_stars' AND provider_payment_charge_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_pay_reconciliation
  ON payments (status, reconciliation_reason, created_at)
  WHERE status IN ('refunding','reconciliation');
CREATE INDEX IF NOT EXISTS idx_pay_refund_state
  ON payment_refunds (state, updated_at);
CREATE INDEX IF NOT EXISTS idx_sub_payment_source
  ON subscriptions (payment_id, status)
  WHERE payment_id IS NOT NULL;
