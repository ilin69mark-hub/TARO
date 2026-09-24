-- 019: промежуточный статус refunding (аудит C: FOR UPDATE через внешний TG-вызов
-- держал лок секундами; теперь двухфазно без долгого лока).
DO $$
DECLARE c text;
BEGIN
  SELECT conname INTO c FROM pg_constraint
   WHERE conrelid = 'payments'::regclass AND contype = 'c'
     AND pg_get_constraintdef(oid) LIKE '%refunded%';
  IF c IS NOT NULL THEN
    EXECUTE format('ALTER TABLE payments DROP CONSTRAINT %I', c);
  END IF;
END
$$;
ALTER TABLE payments ADD CONSTRAINT payments_status_check
  CHECK (status IN ('pending','succeeded','refunded','expired','refunding'));
