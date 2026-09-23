-- 006_single_any_spread.up.sql — single_99 = 1 чтение ЛЮБОГО premium (см. T29).
-- spread_code='any' гасится первым premium-чтением; FK на spreads(code) снят (plain TEXT).
ALTER TABLE single_entitlements DROP CONSTRAINT single_entitlements_spread_code_fkey;
