-- 040 down: откат переноса покупки.
--
-- ВНИМАНИЕ: строки со status='merged' удаляются. Это осознанно. После
-- слияния проигравшая строка — пустая оболочка: её readings/payments/subscriptions
-- уже переехали к survivor (link.go), и на ней не остаётся ничего, кроме
-- метаданных. Возвращать состояние 'active' нельзя — это воскресило бы
-- аккаунт, которого больше не существует, и его cookie снова стал бы валидным.
--
-- Если откат нужен именно для восстановления доступа после НЕЖЕЛАННОГО
-- слияния (пересланный токен) — это делает не миграция, а
-- POST /v1/admin/auth/handoff/rollback: он переносит строки ОБРАТНО по
-- журналу auth_handoffs, где записан survivor_tg_id.

DELETE FROM users WHERE status = 'merged';

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_status_check;
ALTER TABLE users ADD CONSTRAINT users_status_check
  CHECK (status = ANY (ARRAY['active'::text, 'disabled'::text, 'deleted'::text]));

DROP TABLE IF EXISTS auth_handoffs;

UPDATE app_config
   SET value = value - 'handoff_enabled'
 WHERE key = 'auth';
