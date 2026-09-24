-- 022: дедуплика пуш-рассылок (аудит D: remind слал 3 дня подряд, логи всегда sent).
-- Один kind на юзера в сутки (MSK). NULL user_id не конфликтуют (удаленные юзеры).
CREATE UNIQUE INDEX IF NOT EXISTS idx_push_logs_dedup
  ON push_logs (user_id, kind, ((created_at AT TIME ZONE 'Europe/Moscow')::date));
-- Ретеншен: cron чистит старше 90 дней (см. deploy cron).
