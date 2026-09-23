# Взаимодействие Next.js → Go → PG/Redis/OpenRouter

> Статус: `frozen v1.0 (2026-09-23)`. Связи: `04-api-spec.md`, `05-cache-redis.md`, `06-ai-pipeline.md`.

## Диаграмма последовательности (чтение)
```
User → web (Next): POST /api/readings (proxy) → api-public :8080/v1/readings [JWT cookie]
api → Redis: Lua INCR ent:<user>:<date> + GET spreads:list (проверка лимита, 5мс)
api → PG: INSERT readings(status=pending, seed, algo_version=1) + SELECT cards/spread
api → OpenRouter: POST chat/completions (stream) [req timeout 8с ×2 ретрая, total SSE 25с]
api ─SSE→ web ─SSE→ User (токены) ; по завершении UPDATE readings(status=done)
```
Админ: `browser → web :3000/admin → server-side proxy (Next route, JWT taro_admin) → 127.0.0.1:8081/v1/admin/*`. Прямого доступа браузера к 8081 нет.

## Правила
- web никогда не ходит в PG/Redis/OpenRouter напрямую — только через Go.
- Публичный трафик: `web :3000 → api-public :8080`. Админ-трафик — только через server-side proxy (см. выше), JWT `taro_admin`.
- Ключ OpenRouter только в env api (`OPENROUTER_API_KEY`), в web его нет.
- SSE: `api` стримит, `web` проксирует (`/app/api/.../route.ts` с `no-store`).
- Таймауты едино: `8с request timeout ×2 ретрая + 25с total SSE` → fallback (см. `06-ai-pipeline`).
- Идемпотентность: header `Idempotency-Key` главный (поле в теле — fallback); PG `UNIQUE(user_id, key)`; повтор при `status=pending` возвращает тот же `reading_id` с докачкой `GET /v1/readings/:id`.

## Ошибки (единый формат)
`{error:{code, message_ru, details?}}` — коды едино: `LIMIT_EXCEEDED(402)`, `INVALID_TG_HASH(401)`, `BAD_SIGN(401, только Stars webhook)`, `AI_TIMEOUT(504, с fallback)`, `VALIDATION(422)`, `RATE_LIMITED(429)`.
