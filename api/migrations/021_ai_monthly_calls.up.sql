-- 021: monthly_calls в ai-конфиг (kill-switch был выключен: 0 = unlimited).
-- Трогаем только строки без ключа (ручные настройки админа не затираем).
UPDATE app_config
   SET value = value || '{"monthly_calls": 5000}'::jsonb, updated_at = now()
 WHERE key = 'ai' AND NOT (value ? 'monthly_calls');
