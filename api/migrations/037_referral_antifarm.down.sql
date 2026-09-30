-- 037 down: откатываем антиферму рефералки.
--
-- ВНИМАНИЕ: down УДАЛЯЕТ накопленные сигналы (fingerprint/IP) и причины отказа.
-- Они не восстанавливаются: для строк, созданных после 037, после отката
-- антиферма снова станет «есть tg_id» и merge снова сможет перенести pending.
-- Если нужна точность, откатывайте на резервной копии (backup → migrate →
-- rollback, порядок в deploy/README.md).
--
-- Колонка code и её UNIQUE НЕ трогаем: она существовала до 037.

DROP INDEX IF EXISTS idx_referrals_pending;

ALTER TABLE referrals
  DROP COLUMN IF EXISTS reject_reason,
  DROP COLUMN IF EXISTS referrer_ip,
  DROP COLUMN IF EXISTS referrer_fp,
  DROP COLUMN IF EXISTS referee_ip,
  DROP COLUMN IF EXISTS referee_fp,
  DROP COLUMN IF EXISTS applied_code;

ALTER TABLE users
  DROP COLUMN IF EXISTS referral_ip;
