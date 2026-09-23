-- 015_admin_audit_nullable.down.sql
-- Обратно только если нет NULL-строк.
ALTER TABLE admin_audit ALTER COLUMN admin_id SET NOT NULL;
