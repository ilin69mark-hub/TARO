ALTER TABLE admin_accounts
  ADD COLUMN session_version BIGINT NOT NULL DEFAULT 1 CHECK (session_version > 0);
