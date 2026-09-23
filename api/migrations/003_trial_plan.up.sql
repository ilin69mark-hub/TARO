-- 003_trial_plan.up.sql — trial-тариф для бонусов (см. 02-functional/05, T08/T09).
-- Trial пишется в subscriptions как обычный план (единственный источник valid_until).
ALTER TABLE plans DROP CONSTRAINT plans_code_check;
ALTER TABLE plans ADD CONSTRAINT plans_code_check
  CHECK (code IN ('free', 'month_299', 'year_2490', 'single_99', 'trial_3d'));

INSERT INTO plans (code, price_rub, stars_amount, duration_days, is_active) VALUES
  ('trial_3d', 0, 0, 3, true);
