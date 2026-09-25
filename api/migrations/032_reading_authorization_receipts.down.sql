DO $$
BEGIN
  RAISE EXCEPTION '032 down refused: authorization receipts are forward-only';
END
$$;
