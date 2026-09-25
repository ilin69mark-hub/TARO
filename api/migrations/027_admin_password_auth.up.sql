CREATE TABLE admin_accounts (
  user_id UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  username TEXT NOT NULL UNIQUE CHECK (username = lower(username) AND char_length(username) BETWEEN 3 AND 64),
  password_hash TEXT NOT NULL CHECK (char_length(password_hash) BETWEEN 59 AND 100),
  is_active BOOLEAN NOT NULL DEFAULT true,
  last_login_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
