-- 036 down: откатываем колонку-маркер и возвращаем исходный unique-индекс
-- (A12/F-12).
--
-- ВНИМАНИЕ: down не может «отменить отмену» — уже возвращённые слоты не
-- вернутся обратно в списанное состояние. Это не потеря: возвращённый слот
-- означает «пользователь не получил чтение», и после отката миграции новые
-- провалы снова будут съедать слоты, но уже записанные отмены останутся.
-- Откатывать 036 имеет смысл только вместе с откатом кода, который её вызывает.
--
-- Сначала схлопываем дубли по entitlement_id: часть индексов в 036 создана
-- как частичная (WHERE released_at IS NULL), поэтому в таблице могут лежать
-- несколько квитанций на одну single-покупку. Исходный unique-индекс такую
-- пару не пропустит, и оставляем самую свежую квитанцию (освобождение,
-- записанное последним, и есть актуальный факт).

DELETE FROM reading_authorization_receipts older
 USING reading_authorization_receipts newer
 WHERE older.entitlement_id = newer.entitlement_id
   AND older.entitlement_id IS NOT NULL
   AND (older.created_at, older.reading_id) < (newer.created_at, newer.reading_id);

ALTER TABLE reading_authorization_receipts
  DROP COLUMN IF EXISTS released_at;

ALTER TABLE readings
  DROP COLUMN IF EXISTS failure_reason,
  DROP COLUMN IF EXISTS failed_at;

CREATE UNIQUE INDEX idx_reading_authorization_receipts_entitlement
  ON reading_authorization_receipts (entitlement_id)
  WHERE entitlement_id IS NOT NULL;
