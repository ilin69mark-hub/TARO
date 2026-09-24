-- 020: токены шеринга (приватные ссылки /share/:token без PII в URL).
-- Без толкования: его отдаёт только владелец; Сам токен — opaque 32 hex.
CREATE TABLE IF NOT EXISTS share_tokens (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  reading_id UUID NOT NULL UNIQUE REFERENCES readings (id) ON DELETE CASCADE,
  token TEXT NOT NULL UNIQUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_share_token ON share_tokens (token);
