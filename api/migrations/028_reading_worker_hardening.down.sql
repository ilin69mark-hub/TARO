DO $$
BEGIN
  RAISE EXCEPTION '028 down refused: quota state is forward-only';
END
$$;
