-- 003_trial_plan.down.sql — только деактивация (DELETE ломался о subscriptions.plan_id FK → dirty).
UPDATE plans SET is_active = false WHERE code = 'trial_3d';
