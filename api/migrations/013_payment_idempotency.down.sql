-- 013_payment_idempotency.down.sql
DROP INDEX IF EXISTS idx_pay_user_idem;
ALTER TABLE payments DROP COLUMN idempotency_key;
