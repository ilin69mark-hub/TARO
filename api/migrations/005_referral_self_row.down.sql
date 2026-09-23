-- 005_referral_self_row.down.sql
ALTER TABLE referrals ADD CONSTRAINT referrals_check CHECK (referrer_id != referee_id);
