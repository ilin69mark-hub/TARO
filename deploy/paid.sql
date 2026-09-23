-- paid.sql — дашборд критерия натива (см. V27, 07-roadmap/03).
-- Цель: 1000 active-платных. MRR/churn — см. 07-roadmap/04-kpi.md.
SELECT
  (SELECT COUNT(DISTINCT user_id) FROM subscriptions
    WHERE status='active' AND valid_until > now() AND plan_code IN ('month_299','year_2490')) AS paid_active,
  1000 AS native_gate,
  (SELECT COUNT(DISTINCT user_id) FROM subscriptions
    WHERE status='active' AND valid_until > now() AND plan_code='trial_3d') AS trial_active,
  (SELECT COUNT(*) FROM users) AS users_total,
  (SELECT ROUND(100.0 * COUNT(DISTINCT user_id) / 1000, 1) FROM subscriptions
    WHERE status='active' AND valid_until > now() AND plan_code IN ('month_299','year_2490')) AS gate_pct;
