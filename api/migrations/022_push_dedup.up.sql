-- 022: дедуплика пуш-рассылок (аудит D: remind слал 3 дня подряд, логи всегда sent).
-- Один kind на юзера в сутки (MSK). NULL user_id не конфликтуют (удаленные юзеры).
DELETE FROM push_logs older
USING push_logs newer
WHERE older.user_id IS NOT NULL
  AND newer.user_id IS NOT NULL
  AND older.user_id = newer.user_id
  AND older.kind = newer.kind
  AND (older.created_at AT TIME ZONE 'Europe/Moscow')::date = (newer.created_at AT TIME ZONE 'Europe/Moscow')::date
  AND older.ctid < newer.ctid;

CREATE UNIQUE INDEX IF NOT EXISTS idx_push_logs_dedup
  ON push_logs (user_id, kind, ((created_at AT TIME ZONE 'Europe/Moscow')::date));
-- Ретеншен: cron чистит старше 90 дней (см. deploy cron).
