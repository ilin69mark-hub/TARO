-- revenue.sql — выручка по провайдерам за период (см. V20, 07-roadmap/04).
-- Использование: psql -v from=2026-09-01 -v to=2026-10-01 -f deploy/revenue.sql
-- MRR считается отдельно (см. 07-roadmap/04-kpi.md): month_299×299 + year_2490/12.
SELECT provider,
       plan_code,
       COUNT(*) AS payments,
       SUM(amount_rub) AS amount_rub,
       SUM(CASE WHEN note IS NOT NULL THEN 1 ELSE 0 END) AS with_discount
  FROM payments
 WHERE status = 'succeeded'
   AND created_at >= :'from'::date AND created_at < :'to'::date
 GROUP BY 1, 2
 ORDER BY 4 DESC;
