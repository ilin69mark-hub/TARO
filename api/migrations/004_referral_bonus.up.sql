-- 004_referral_bonus.up.sql — план реферальных бонусов (см. 02-functional/06, T14).
ALTER TABLE plans DROP CONSTRAINT plans_code_check;
ALTER TABLE plans ADD CONSTRAINT plans_code_check
  CHECK (code IN ('free', 'month_299', 'year_2490', 'single_99', 'trial_3d', 'referral_bonus'));

INSERT INTO plans (code, price_rub, stars_amount, duration_days, is_active) VALUES
  ('referral_bonus', 0, 0, NULL, true);
