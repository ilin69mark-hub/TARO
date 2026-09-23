-- ai-budget.sql — дневной контроль AI-расходов (см. U01, 04-architecture/06).
-- GWT U01: дневной расход <$5, иначе алерт.
-- Цены OpenRouter (проверять при смене модели, $/1M токенов, input+output усредненно):
--   gpt-4o-mini ≈ $0.15 in / $0.60 out; claude-3-haiku ≈ $0.25 / $1.25.
-- Использование: psql -f deploy/ai-budget.sql (выход: строки дней с cost_usd).
WITH prices(model, pin, pout) AS (
  VALUES
    ('openai/gpt-4o-mini', 0.15, 0.60),
    ('anthropic/claude-3-haiku', 0.25, 1.25)
)
SELECT date_trunc('day', created_at)::date AS day,
       model,
       SUM(tokens_in + tokens_out) AS tokens,
       ROUND((SUM(tokens_in) * MAX(pin) + SUM(tokens_out) * MAX(pout)) / 1000000.0, 4) AS cost_usd,
       CASE WHEN (SUM(tokens_in) * MAX(pin) + SUM(tokens_out) * MAX(pout)) / 1000000.0 > 5
            THEN 'ALERT: больше $5/день' ELSE 'ok' END AS budget
  FROM ai_logs JOIN prices USING (model)
 WHERE status = 'ok'
 GROUP BY 1, 2
 ORDER BY 1 DESC, 2
 LIMIT 30;
