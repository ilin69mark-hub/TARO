-- 014_referral_code_col.down.sql — деструктивный (коды теряются). Только с ALLOW_DESTRUCTIVE=1.
-- migrate.sh блокирует down без флага; здесь страховка через переменную окружения невозможна,
-- поэтому down требует пустую колонку ИЛИ явное подтверждение оператора (см. migrate.sh).
-- Вручную: вернуть коды из users.referral_code перед откатом.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM users WHERE referral_code IS NOT NULL) THEN
    RAISE EXCEPTION 'irreversible down: users.referral_code содержит данные (сделай дамп)';
  END IF;
END
$$;
ALTER TABLE users DROP COLUMN referral_code;
