DROP INDEX IF EXISTS idx_share_tokens_active;

ALTER TABLE share_tokens
  DROP COLUMN IF EXISTS expires_at,
  DROP COLUMN IF EXISTS revoked_at;
