-- 006_single_any_spread.down.sql — IRREVERSIBLE при живых 'any'-строках (весь single_99).
-- Откат только на пустой таблице, иначе FK-violation + dirty.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM single_entitlements WHERE spread_code = 'any') THEN
    RAISE EXCEPTION 'irreversible down: single_entitlements содержит spread_code=any';
  END IF;
END
$$;
ALTER TABLE single_entitlements ADD CONSTRAINT single_entitlements_spread_code_fkey
  FOREIGN KEY (spread_code) REFERENCES spreads (code) ON DELETE RESTRICT;
