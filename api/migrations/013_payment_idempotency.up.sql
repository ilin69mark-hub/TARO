-- 013_payment_idempotency.up.sql — идемпотентность invoice (см. D3).
ALTER TABLE payments ADD COLUMN idempotency_key TEXT NULL;
CREATE UNIQUE INDEX idx_pay_user_idem ON payments (user_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
