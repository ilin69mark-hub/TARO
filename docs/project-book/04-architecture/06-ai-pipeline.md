# AI-пайплайн (OpenRouter)

> Статус: `draft`. Связи: `02-functional/03-reading-flow.md`, `04-architecture/03-database-schema.md` (`ai_logs`).

## Модель (конфиг, админка)
Дефолт: `openai/gpt-4o-mini` через OpenRouter (дешево, быстро, RU хорошо). Fallback: `anthropic/claude-3-haiku`. Параметры: `temperature 0.7, max_tokens 900`. Таймауты едино: `8с request timeout ×2 ретрая + 25с total SSE` → fallback.

## Circuit breaker (пороги)
`5×5xx/1мин → open 5мин → half-open 1 probe → close/open`; состояние в Redis + `ai_logs status=breaker`; юзеру всегда fallback без 500. 2-й ключ `OPENROUTER_API_KEY_2` включается при open.

## Промпт (каркас, v1)
```
system: Ты бережный таролог-психолог. Пишешь по-русски, без запугиваний, без медицины/юриспруденции. Структура: 1) суть 2-3 предл. 2) по каждой карте 2-3 предл. с привязкой к позиции. 3) совет + 2 вопроса для рефлексии. Запрещены слова: смерть/порча/развод неизбежен.
user: Расклад {spread}, позиции {positions}, карты {cards+upright/reversed значения из БД}, вопрос: {question}
```

## Защита от галлюцинаций
- Значения карт подставляем из `cards` (модель не выдумывает арканы).
- Пост-фильтр: если в ответе стоп-слова (`умрешь`, `порча`) → заменяем на безопасный абзац + `ai_logs status=filtered`.
- Суицид/самоповреждение во вопросе → не шлем в AI как есть, отдаем кризисный шаблон + контакты (РФ: 8-800-...).

## Логирование и стоимость
Каждый вызов → `ai_logs {model, prompt_hash (sha256 без PII), tokens, latency, status}`. Бюджет: free 1/день × ~800 токенов ≈ $0.0015/чтение. Алерт если >$5/день.

## Edge
| Кейс | Поведение |
|---|---|
| 5xx/timeout | ретрай ×2 (8с) → `readings.status=pending_fallback` + fallback-текст из БД + worker `UPDATE readings → done + ai_logs` |
| Ключ утек/лимит OpenRouter | breaker (см. выше), юзеру fallback без 500 |
| Повтор тех же карт | `ai:cache:<model>:<spread>:<cards_hash>` без вопроса (PII не кэшируем между юзерами) |
