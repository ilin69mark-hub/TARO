# AI-код, ревью, логи

> Статус: `draft`.
- ChatGPT/Copilot — только черновик. Проверка: `go vet`, прогон GWT руками, `EXPLAIN` для SQL, ревью диффа строкой.
- Логи: Go `slog JSON` (`reading_id`, `user_id`, `latency`), Next — только client-error в `/v1/logs`. Смотрим в Loki/Grafana на VPS, алерты в TG.
- Мониторинг MVP: аптайм `/healthz`, `ai_logs failed/hour`, `payments pending>15м`, Redis mem.
