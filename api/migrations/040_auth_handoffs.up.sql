-- 040: перенос покупки из браузера в Telegram — аудит и состояние 'merged'.
--
-- Зачем эта таблица. Человек платит в обычном браузере, потом приходит в
-- Telegram и не видит своей покупки. Механизм переноса — одноразовый токен,
-- и токен это bearer-секрет: его можно переслать. Привязка к IP отсекает
-- пересылку из другой сети, но не из той же (общий Wi-Fi). Значит, полагаться
-- на невозможность злоупотребления нельзя, и нужна опора на аудит и откат.
--
-- Именно поэтому здесь лежит survivor_tg_id: куда именно ушло слияние.
-- Без него поддержка на вопрос «мой аккаунт куда делся» отвечает только
-- «проверь сам», а с ним отвечает точно и откатывает (см. handoff.rollback).
--
-- Секрет здесь не хранится: живой токен лежит в Redis с TTL 300 с, и по
-- ключу идёт sha256, а не сам токен. Таблица — это след, а не хранилище.

CREATE TABLE IF NOT EXISTS auth_handoffs (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  plan_code      text,
  -- Префикс сети (/24 или /64), а не полный адрес: полный адрес у мобильных
  -- операторов гуляет внутри одной подсети, и честный плательщик получил бы
  -- отказ на ровном месте.
  ip_prefix      text NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  consumed_at    timestamptz,
  -- В какой Telegram-аккаунт ушло слияние. Заполняется при потреблении.
  survivor_tg_id bigint
);

CREATE INDEX IF NOT EXISTS idx_handoffs_user_created
  ON auth_handoffs (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_handoffs_pending
  ON auth_handoffs (created_at DESC) WHERE consumed_at IS NULL;

-- Состояние 'merged' для проигравшей стороны слияния.
--
-- Раньше Link() делал DELETE FROM users. Это необратимо: если слияние
-- произошло с тем аккаунтом, который не должен был его получить (пересланный
-- токен из той же сети), отменить это можно было только бэкапом. Раз мы
-- делаем перенос обратимым, проигравшая строка переживает слияние.
--
-- status не равен 'active', поэтому RequireAuth её не пускает (link.go:60) —
-- старая сессия браузера честно получает 401 и может быть перезагружена.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_status_check;
ALTER TABLE users ADD CONSTRAINT users_status_check
  CHECK (status = ANY (ARRAY['active'::text, 'disabled'::text, 'deleted'::text, 'merged'::text]));

-- anon_uuid с UNIQUE освобождаем: оставленная на merged-строке строка сломала бы
-- пересоздание анонимной личности с тем же uuid из localStorage, то есть
-- после переноса человек не смог бы зайти вообще.
UPDATE users SET anon_uuid = NULL WHERE status = 'merged' AND anon_uuid IS NOT NULL;

-- Выключатель. Фронт всё равно не зовёт перенос, пока не задан
-- NEXT_PUBLIC_TG_APP_URL, но и на бое флаг можно снять без релиза.
INSERT INTO app_config (key, value) VALUES
  ('auth', '{"handoff_enabled":false}'::jsonb)
ON CONFLICT (key) DO UPDATE SET value = app_config.value || EXCLUDED.value;
