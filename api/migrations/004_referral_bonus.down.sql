-- 004_referral_bonus.down.sql
DELETE FROM plans WHERE code = 'referral_bonus';
ALTER TABLE plans DROP CONSTRAINT plans_code_check;
ALTER TABLE plans ADD CONSTRAINT plans_code_check
  CHECK (code IN ('free', 'month_299', 'year_2490', 'single_99', 'trial_3d'));
