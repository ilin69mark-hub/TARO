-- 035: одна строка subscriptions на платёж + защита от lost update (A07/F-07).
--
-- Проблема: grantPaymentEntitlements читал MAX(valid_until) и вставлял новую
-- строку. Два конкурентных вебхука по разным платежам одного юзера блокируют
-- разные строки payments, поэтому оба читали один MAX и оба считали прибавку
-- от него — два оплаченных месяца давали 30 дней. Плюс повторная обработка
-- одного платежа давала две строки на один payment_id.
--
-- Что делаем:
--   1) выдача сериализуется advisory-блокировкой по юзеру (код: payments.go,
--      grantPaymentEntitlements) — правку БД она не заменяет, а фиксирует;
--   2) уникальный индекс по payment_id — нижний уровень гарантии: одна
--      подписка на платёж, независимо от гонок.
--
-- Дубли, которые гонка успела создать, схлопываем ДО создания индекса, иначе
-- он не применится на грязной базе. При равенстве valid_until и разном
-- payment_id ничего не удаляется: у анонимных бонусов (referral/trial) он
-- NULL, дублей на платёж там быть не может.

DELETE FROM subscriptions s
 USING subscriptions keep
 WHERE s.payment_id IS NOT NULL
   AND s.payment_id = keep.payment_id
   AND (s.valid_until < keep.valid_until
        OR (s.valid_until = keep.valid_until AND s.id > keep.id));

CREATE UNIQUE INDEX IF NOT EXISTS idx_sub_payment_uniq
  ON subscriptions (payment_id)
  WHERE payment_id IS NOT NULL;
