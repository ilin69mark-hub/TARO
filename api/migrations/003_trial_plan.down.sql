-- 003_trial_plan.down.sql
DELETE FROM plans WHERE code = 'trial_3d';
ALTER TABLE plans DROP CONSTRAINT plans_code_check;
ALTER TABLE plans ADD CONSTRAINT plans_code_check
  CHECK (code IN ('free', 'month_299', 'year_2490', 'single_99'));
