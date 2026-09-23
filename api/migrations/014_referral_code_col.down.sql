-- 014_referral_code_col.down.sql
-- Восстановить self-строки нельзя (коды утеряны частично) — down только для пустой таблицы.
-- Вручную: вернуть коды из users.referral_code перед откатом.
ALTER TABLE users DROP COLUMN referral_code;
