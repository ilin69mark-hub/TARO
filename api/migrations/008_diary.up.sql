-- 008_diary.up.sql — дневник рефлексии (см. V05, Could мес.4–6).
CREATE TABLE diary_entries (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  reading_id UUID NULL REFERENCES readings (id) ON DELETE SET NULL,
  body TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 10000),
  mood TEXT NULL CHECK (mood IN ('up', 'down', 'calm', 'anxious', 'grateful')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_diary_user_created ON diary_entries (user_id, created_at DESC);
CREATE INDEX idx_diary_user_mood ON diary_entries (user_id, mood) WHERE mood IS NOT NULL;
