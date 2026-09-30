-- 039: antifarm_ip выключается по умолчанию (требование к окружению, не осторожность).
--
-- Почему. IP в рефералке берётся из X-Real-IP, который nginx ставит из
-- $remote_addr, а set_real_ip_from в deploy/nginx.conf НЕ настроен. Как только
-- перед nginx встанет Cloudflare (это уже запланировано — E04 «Cloudflare CDN;
-- домен taro.me»), $remote_addr станет edge-IP Cloudflare, одинаковым для ВСЕХ
-- посетителей. Тогда проверка «реферер и рефёр пришли с одного адреса» начнёт
-- отклонять практически все легитимные рефералки — тихо, без ошибок, просто
-- бонус не придёт.
--
-- Тот же IP попадает в ключ ratelimit для маршрутов с byUser=false
-- (/v1/auth/, /v1/spreads, /v1/share) — но это отдельная, не реферальная
-- проблема, и она не входит в эту миграцию.
--
-- Второй источник ложных отказов — CGNAT мобильных операторов: десятки
-- честных пользователей за одним адресом.
--
-- Носитель защиты теперь fingerprint: парная проверка referrer_fp==referee_fp
-- плюс кластерная (fingerprint_cluster_limit). Обе работают без IP.
--
-- ВКЛЮЧАТЬ antifarm_ip=true только после того, как в nginx появится real_ip
-- (set_real_ip_from со списком IP-диапазонов Cloudflare + real_ip_header
-- CF-Connecting-IP) и это проверено на живом стенде. Шаг задеплоен в
-- docs/BACKLOG_OWNER.md (D8).

INSERT INTO app_config (key, value) VALUES
  ('referral', '{"antifarm_ip":false}'::jsonb)
ON CONFLICT (key) DO UPDATE SET value = app_config.value || EXCLUDED.value;
