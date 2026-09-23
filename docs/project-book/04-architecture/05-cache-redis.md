# Redis: что кешируем и на сколько

> Статус: `draft`. Связи: `02-interaction-next-go.md`.

| Ключ | Значение | TTL | Инвалидация |
|---|---|---|---|
| `sess:<user_id>` | refresh hash (единый, `refresh:` удален) | 30д user / 12ч admin | `POST /v1/auth/logout` |
| `ent:<user>:<YYYY-MM-DD>` | free_used count | `EXPIREAT 00:00 Europe/Moscow` | точечно `DEL ent:<user>:<date>` |
| `ent:<user>:love:<YYYY-WW>` | love_used_week | до конца ISO-недели МСК | точечно |
| `spreads:list:v1` | JSON активных раскладов | 5 мин | `POST /v1/admin/config/publish` → `DEL spreads:list:v1` (<5с; без Publish ≤5 мин) |
| `plans:active:v1` | JSON тарифов | 5 мин | так же `DEL plans:active:v1` |
| `ai:cache:<model>:<spread>:<cards_hash>` | толкование без вопроса (переиспользование между юзерами безопасно) | 7 дней | при смене модели ключ другой |
| `idem:<user>:<key>` | reading_id | 24ч | PG `UNIQUE(user_id,key)` — истина вечно |
| `rl:<ip>:<route>` | счетчик leaky bucket | 1 мин/час | — |

## Правила
- Источник правды — PG. Redis дропать запрещено для `ent:*`/`idem:*` (дроп = безлимит). Потеря кэша spreads/plans/AI — безопасна.
- `free_used/love_used` — только атомарный Lua `INCR+EXPIREAT+сравнение`; nightly job `PG.free = MAX(PG, Redis GET)`.
- Publish → точечные `DEL` (никаких `KEYS/spreads*` — блокирует Redis, использовать `UNLINK` точечно).
- AI-кэш строго без `q_hash` (сырой вопрос = PII, сливает чужие формулировки) — только `(model,spread,cards)`; per-user вопросы не кэшируются между юзерами.
- Мониторинг: `used_memory < 200MB` на VPS, eviction `allkeys-lru`.
