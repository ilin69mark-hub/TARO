DO $$
DECLARE
  constraint_name text;
BEGIN
  IF to_regclass('public.payment_webhook_events') IS NULL
     OR to_regclass('public.payments') IS NULL THEN
    RAISE EXCEPTION '033 requires payment_webhook_events and payments from migration 029';
  END IF;
  FOR constraint_name IN
    SELECT c.conname
    FROM pg_constraint c
    WHERE c.conrelid = to_regclass('public.payment_webhook_events')
      AND c.contype = 'f'
      AND c.confrelid = to_regclass('public.payments')
  LOOP
    EXECUTE format('ALTER TABLE payment_webhook_events DROP CONSTRAINT %I', constraint_name);
  END LOOP;
END
$$;

CREATE INDEX IF NOT EXISTS idx_payment_webhook_events_payment_created
  ON payment_webhook_events (payment_id, created_at DESC);
