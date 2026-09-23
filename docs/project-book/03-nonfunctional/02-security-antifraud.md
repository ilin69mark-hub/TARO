# Security + Antifraud

> Статус: `draft`. Связи: `02-functional/02-auth.md`, `04-architecture/04-api-spec.md`.

## API-защита
- JWT cookie: `HttpOnly; Secure; SameSite=None + CSRF-header на POST; Path=/; Max-Age 30д user / 12ч admin` (Lax ломает TG WebApp iframe — используем None+CSRF). CORS allowlist только свой домен.
- Rate limit: единый источник — таблица в `04-architecture/04-api-spec.md` (`auth 20/мин/IP` + `anon 20/час/IP + капча`); nginx `limit_req` + Go-mw по IP/user/admin_id.
- Валидация: вопрос ≤500 Unicode code points (NFC), `spread_code` из allowlist активных, `initData` — HMAC SHA256 с bot token; Stars-webhook — отдельно `Secret-Token` header + IP TG (не путать с HMAC).
- Секреты только env/VPS, `.env` в git запрещен (CI fail). Ключ OpenRouter ротируется (2-й ключ `OPENROUTER_API_KEY_2`).

## Анти-накрутка free
| Сигнал | Порог | Действие |
|---|---|---|
| reg anon с 1 IP | >20/час | 429 + fingerprint-капча |
| 1 fingerprint → N uuid | >5/день | склейка, лимит общий |
| эмулятор/TG-hash fail | >10/час/IP | бан IP 1ч |
| реферал сам себе | fingerprint match | `rejected` |

Логи: `security_events` в Loki, алерт в TG-бота админа.

## V31-reaudit (2026-09-23)
- Go rate-middleware добавлен (второй рубеж после nginx): auth 20/мин/IP, readings 10/мин/user, spreads 60/мин/IP, admin 30/мин/admin_id (см. `internal/ratelimit`, e2e 429).
- Секретов в коде нет (grep), `.env` в gitignore, CSRF глобален (кроме Stars-webhook с Secret-Token), admin :8081 unpublished.
- npm audit: next@14.2.35 (последний 14.2.x) имеет GHSA-DoS; апгрейд до 16 невозможен (React 18 + fiber@8 пины, см. 08-ultra). Митигации: нет remotePatterns/rewrites, images только local, nginx+Go лимиты спереди. Пересмотреть при смене fiber.
- Повторять реаудит каждый месяц + при добавлении провайдера.
