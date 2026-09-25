DO $$
BEGIN
  RAISE EXCEPTION '031 down refused: quota repair is forward-only';
END
$$;
