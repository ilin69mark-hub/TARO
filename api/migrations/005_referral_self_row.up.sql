-- 005_referral_self_row.up.sql — self-строка хранит код юзера (referrer=referee),
-- поэтому CHECK referrer!=referee невозможен. Самореферал запрещает код apply (см. T14).
ALTER TABLE referrals DROP CONSTRAINT referrals_check;
