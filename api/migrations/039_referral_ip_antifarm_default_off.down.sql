-- 039 down: возвращаем antifarm_ip=true (исходное значение 038/037).
--
-- ВНИМАНИЕ: возвращать только если в nginx настроен real_ip. Иначе под
-- Cloudflare все посетители получат один $remote_addr и проверка по IP
-- начнёт отклонять легитимные рефералки. См. up-файл и docs/BACKLOG_OWNER.md (D8).

UPDATE app_config
   SET value = jsonb_set(value, '{antifarm_ip}', 'true'::jsonb)
 WHERE key = 'referral';
