-- 010_seasonal_codes.down.sql
DELETE FROM spreads WHERE code IN ('fullmoon', 'newyear');
ALTER TABLE spreads DROP CONSTRAINT spreads_code_check;
ALTER TABLE spreads ADD CONSTRAINT spreads_code_check
  CHECK (code IN ('daily', 'three', 'love', 'decision', 'celtic'));
