-- 015_admin_audit_nullable.up.sql — cron-действия без user_id (см. S10).
ALTER TABLE admin_audit ALTER COLUMN admin_id DROP NOT NULL;
