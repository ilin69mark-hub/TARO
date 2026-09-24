# BACKLOG «Онлайн Таро» — web-only PWA (натив запрещен до 1000 платных, ADR-05)

> Источник правды — `docs/project-book/`. Статусы: `- [ ] todo` → `🔄 doing` → `- [x] done (дата)` / `BLOCKED`.
> Правила: 1 задача = 1 ветка `feat/Txx-...` = 1 PR по DoD (`06-rules/03-dod-frozen-scope.md`).
> KPI-гейт нед.7: 50 друзей, ≥80 чтений, 0×500 на paywall, LCP лендинга <2.5s + LCP 3D <4.0/<5.0s.
> Стоп-кран рекламы: `spread_open→reading_done <60%`, `reading→paywall >30% без pay`, `paywall→pay <2%` — стопаем.
> Полная история задач — в git (`BACKLOG.md` на коммитах до чистки 2026-09-23).

## Итоги MVP (T01–T30 done 2026-09-23, детали — в git-истории)
- **Эпик 0 + Нед.1 (T01–T07):** монорепо + CI, compose-стек, миграции пакета A (13 таблиц), сиды (78 карт, 5 спредов, 4 тарифа), spreads API + Redis, каркас 5 экранов кодом, аналитика-спека. Уроки: Go 1.25 (pgx/redis), FK plans — денормализация, `admin-api` только internal.
- **Нед.2 (T08–T15):** auth TG+anon+Link/trial, sess/CSRF, EntitlementsService (Lua), readings SSE + идемпотентность + кризис, AI-gateway + breaker + worker, рефералка с антифродом, DeleteMe + 18+. Уроки: merge-транзакция, trial одноразовый, Redis-дроп запрещен.
- **Нед.3 (T16–T20):** proxy /api, auth-bootstrap, SSE-клиент, история + locked, paywall из конфига, PWA (manifest/SW/install), юр-экраны. Уроки: API_INTERNAL_URL, `.next` копировать после rm, image коммитить.
- **Нед.4–5 (T21–T24):** R3F-база + туман, foil-карты + tilt, пост-эффекты + ACES, арт-контракт (генерация — ручной шаг владельца).
- **Нед.6 (T25–T28):** храм (reflector desktop), свет static, пыль 600/150/0, камера + FPS-гард + a11y. Замер на эталонах — ручной шаг.
- **Нед.7 (T29–T30):** Stars (invoice/webhook/verify/refund, single='any'), migrate.sh с трекингом, backup 30д, deploy по тегам. Живые TG/OpenRouter-ключи — на проде.
- **Пост-аудит:** пакеты A/B/C + D + SEC S01–S10 (см. `BACKLOG_SEC.md`), покрытие Go 70.4% + web 14/14.

## Открытые по этой серии
_Нет — все T01–T30 закрыты. Открытые — в `BACKLOG_GROWTH.md` (U), `BACKLOG_V46.md` (V), `BACKLOG_SEC.md` (повторный аудит — по запросу), `BACKLOG_OWNER.md` (E01–E24, владелец)._

## Остатки для прода (только с живыми ключами/железом, не код)
- OPENROUTER_API_KEY → живой AI-тест; TG_BOT_TOKEN + Stars → живой платеж
- VPS: UFW/ss-check, cron backup, замер FPS на iPhone 12 + Moto G54
- Генерация 22 артов (промпты в `docs/cards/`) + лицензия в links
