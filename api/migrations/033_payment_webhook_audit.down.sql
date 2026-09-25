DO $$
BEGIN
  RAISE EXCEPTION '033 down refused: payment webhook audit retention is forward-only';
END
$$;
