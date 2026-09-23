-- 014_referral_code_col.up.sql — коды в users.referral_code (см. D-баг: self-строки
-- конфликтовали с UNIQUE(referee_id) и блокировали apply всем, кто открыл /referral/me).
ALTER TABLE users ADD COLUMN referral_code TEXT NULL;
CREATE UNIQUE INDEX idx_users_refcode ON users (referral_code) WHERE referral_code IS NOT NULL;
-- перенос существующих кодов из self-строк
UPDATE users u SET referral_code = r.code
  FROM referrals r WHERE r.referrer_id = u.id AND r.referee_id = u.id;
DELETE FROM referrals WHERE referrer_id = referee_id;
