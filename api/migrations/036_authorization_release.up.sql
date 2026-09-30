-- 036: отмена списания entitlement при терминальном провале (A12/F-12).
--
-- Проблема: дневной слот (и love/week, и single) списывался в
-- AuthorizeReading ДО вызова провайдера. Если генерация терминально упала
-- (сталив, попытки исчерпаны, pending_fallback → failed), слот оставался
-- потраченным, а пользователь не получал чтения — платил или терял
-- бесплатный слот впустую.
--
-- Что делаем: 1) фиксируем факт отмены в квитанции авторизации (released_at),
--   2) возвращаем слоты: daily/love — декремент счётчиков, single — снимаем
--   consumed_reading_id, чтобы покупка снова была доступна.
--
-- Идемпотентность: повторный вызов отмены ничего не делает (WHERE released_at
-- IS NULL); та же квитанция больше не защищает entitlement от повторного
-- списания (см. receiptVerdictTx) — освобождённый слот можно честно потратить
-- заново на новую попытку.
--
-- Индекс по entitlement_id становится частичным: он защищает от двойного
-- списания ТОЛЬКО пока квитанция активна (released_at IS NULL). Иначе
-- покупка single_entitlements, однажды освобождённая после провала, больше не
-- смогла бы быть использована ни разу — уникальный индекс держал бы вечную
-- блокировку на entitlement_id.
--
-- Плюс причина терминального провала: клиент должен видеть, ЧТО случилось
-- (иначе UI показывает просто «failed» и не может предложить бесплатный
-- ретрай, хотя слот уже возвращён). Текст ошибки провайдера наружу не
-- отдаётся — только машиночитаемый код.

ALTER TABLE reading_authorization_receipts
  ADD COLUMN IF NOT EXISTS released_at TIMESTAMPTZ NULL;

COMMENT ON COLUMN reading_authorization_receipts.released_at IS
  'A12/F-12: чтение терминально упало, entitlement возвращён';

ALTER TABLE readings
  ADD COLUMN IF NOT EXISTS failure_reason TEXT NULL,
  ADD COLUMN IF NOT EXISTS failed_at TIMESTAMPTZ NULL;

COMMENT ON COLUMN readings.failure_reason IS
  'A12/F-12: машиночитаемая причина терминального провала (например provider_failed)';
COMMENT ON COLUMN readings.failed_at IS
  'A12/F-12: момент терминального провала чтения';

DROP INDEX IF EXISTS idx_reading_authorization_receipts_entitlement;

CREATE UNIQUE INDEX idx_reading_authorization_receipts_entitlement
  ON reading_authorization_receipts (entitlement_id)
  WHERE entitlement_id IS NOT NULL AND released_at IS NULL;

COMMENT ON INDEX idx_reading_authorization_receipts_entitlement IS
  'A12/F-12: не более одной активной квитанции на single-покупку; освобождённые (released_at) не учитываются';
