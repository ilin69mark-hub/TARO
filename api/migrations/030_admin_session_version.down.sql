LOCK TABLE admin_accounts IN ACCESS EXCLUSIVE MODE;

DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM admin_accounts
    WHERE session_version <> 1
  ) THEN
    RAISE EXCEPTION '030 down refused: admin session versions have rotated';
  END IF;
END
$$;

ALTER TABLE admin_accounts
  DROP COLUMN IF EXISTS session_version;
