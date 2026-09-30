-- 038 down: убираем только кластерные пороги из конфига.
-- Остальные ключи рефералки (bonus_days, monthly_cap, lifetime_cap_days,
-- antifarm_fp, antifarm_ip) остаются: они пришли из 037 и её откатом не
-- управляются. jsonb - оператор удаляет ключи по имени.

UPDATE app_config
   SET value = value - 'fingerprint_cluster_limit' - 'ip_cluster_limit'
 WHERE key = 'referral';
