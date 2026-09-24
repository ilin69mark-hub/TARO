-- 018: статус cancelled для проигранных single-race (аудит B: UPDATE падал о CHECK).
-- Имя CHECK-ограничения автосгенерировано, снимаем по содержимому дефиниции.
DO $$
DECLARE c text;
BEGIN
  SELECT conname INTO c FROM pg_constraint
   WHERE conrelid = 'readings'::regclass AND contype = 'c'
     AND pg_get_constraintdef(oid) LIKE '%pending_fallback%';
  IF c IS NOT NULL THEN
    EXECUTE format('ALTER TABLE readings DROP CONSTRAINT %I', c);
  END IF;
END
$$;
ALTER TABLE readings ADD CONSTRAINT readings_status_check
  CHECK (status IN ('pending','done','pending_fallback','failed','filtered','cancelled'));
