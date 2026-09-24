-- 021 down: убираем ключ (поведение вернётся к 0 = unlimited).
UPDATE app_config
   SET value = value - 'monthly_calls', updated_at = now()
 WHERE key = 'ai';
