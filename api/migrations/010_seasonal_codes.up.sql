-- 010_seasonal_codes.up.sql — сезонные коды раскладов (см. V21).
ALTER TABLE spreads DROP CONSTRAINT spreads_code_check;
ALTER TABLE spreads ADD CONSTRAINT spreads_code_check
  CHECK (code IN ('daily', 'three', 'love', 'decision', 'celtic', 'fullmoon', 'newyear'));
