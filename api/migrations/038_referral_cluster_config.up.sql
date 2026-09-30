-- 038: доведение конфига рефералки (данные, не схема).
--
-- 037 добавила lifetime_cap_days/antifarm_*, но кластерные пороги
-- (fingerprint_cluster_limit, ip_cluster_limit) дописаны в неё уже после
-- применения на этой базе, поэтому в app_config их нет. Код берёт дефолты
-- (3 и 0) и работает корректно, но админка показывала бы неполный набор
-- ключей, а включить ip_cluster_limit было бы нечем.
--
-- upsert через '||' дописывает ТОЛЬКО новые ключи: значения bonus_days/
-- monthly_cap/lifetime_cap_days, если их меняли в админке, не затираются.

INSERT INTO app_config (key, value) VALUES
  ('referral', '{"fingerprint_cluster_limit":3,"ip_cluster_limit":0}'::jsonb)
ON CONFLICT (key) DO UPDATE SET value = app_config.value || EXCLUDED.value;
