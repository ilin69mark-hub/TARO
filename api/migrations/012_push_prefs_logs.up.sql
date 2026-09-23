-- 012_push_prefs_logs.up.sql — настройки и лог пушей (см. V24, V26).
CREATE TABLE push_preferences (
  user_id UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  hour INT NOT NULL DEFAULT 21 CHECK (hour BETWEEN 0 AND 23),
  quiet BOOL NOT NULL DEFAULT false,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE push_logs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NULL REFERENCES users (id) ON DELETE SET NULL,
  kind TEXT NOT NULL,
  status TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_push_logs_kind_created ON push_logs (kind, created_at DESC);
