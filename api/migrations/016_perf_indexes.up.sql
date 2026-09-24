-- 016: индексы под пер-юзер выборки (аудит B: seq-scan → DoS/тормоза).
CREATE INDEX IF NOT EXISTS idx_pay_user_created ON payments (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_referrals_referrer ON referrals (referrer_id);
CREATE INDEX IF NOT EXISTS idx_push_logs_user ON push_logs (user_id);
