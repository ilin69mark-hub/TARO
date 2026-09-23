-- 006_single_any_spread.down.sql
-- Восстановить FK нельзя без чистки 'any'-строк — down только для пустой таблицы.
ALTER TABLE single_entitlements ADD CONSTRAINT single_entitlements_spread_code_fkey
  FOREIGN KEY (spread_code) REFERENCES spreads (code) ON DELETE RESTRICT;
