-- 004_referral_bonus.down.sql — только деактивация (DELETE ломался о subscriptions.plan_id FK → dirty).
UPDATE plans SET is_active = false WHERE code = 'referral_bonus';
