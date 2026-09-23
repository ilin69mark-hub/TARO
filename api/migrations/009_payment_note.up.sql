-- 009_payment_note.up.sql — след скидок/причин в платеже (см. V17).
ALTER TABLE payments ADD COLUMN note TEXT NULL;
