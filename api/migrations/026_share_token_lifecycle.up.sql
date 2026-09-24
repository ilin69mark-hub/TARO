ALTER TABLE share_tokens
  ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ NOT NULL DEFAULT (now() + interval '30 days'),
  ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_share_tokens_active ON share_tokens (token) WHERE revoked_at IS NULL;
