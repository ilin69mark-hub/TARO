-- 002_seed.down.sql — ЗАПРЕЩЁН на непустой БД: сносит каталоги под живыми FK
-- (readings.spread_code RESTRICT, payments/subscriptions.plan_id) → полуоткат + dirty.
-- Откат только вручную на dev-пустышке; на проде — только migrate.sh up.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM readings) OR EXISTS (SELECT 1 FROM payments) THEN
    RAISE EXCEPTION 'seed down forbidden: живые readings/payments (только dev-пустышка вручную)';
  END IF;
END
$$;
DELETE FROM cards;
DELETE FROM spreads;
DELETE FROM plans;
DELETE FROM app_config;
DELETE FROM users WHERE id = '00000000-0000-0000-0000-000000000001';
