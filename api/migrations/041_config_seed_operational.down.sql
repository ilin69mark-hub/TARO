-- Откат только посеянных здесь строк. auth.handoff_enabled не трогаем: он
-- принадлежит миграции 040.
DELETE FROM app_config WHERE key IN ('ab.price_month','offers.winback','spreads.seasonal','safety.crisis');
