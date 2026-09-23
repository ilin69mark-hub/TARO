-- 011_seasonal_seed.down.sql
DELETE FROM spreads WHERE code IN ('fullmoon', 'newyear');
