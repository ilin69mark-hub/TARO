DO $$
BEGIN
  RAISE EXCEPTION '034 down refused: quota quarantine and terminal guard are forward-only';
END
$$;
