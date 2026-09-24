-- 017: несгораемый реестр trial (аудит B: DELETE CASCADE стирал историю → вечный trial).
-- Строка переживает удаление юзера (SET NULL), повторная выдача невозможна.
CREATE TABLE IF NOT EXISTS trial_grants (
  tg_id BIGINT PRIMARY KEY,
  fingerprint TEXT NOT NULL DEFAULT '',
  user_id UUID NULL REFERENCES users (id) ON DELETE SET NULL,
  granted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
