# Админ-кабинет настроек (твое требование: «сам настраиваю»)

> Статус: `frozen v1.0 (2026-09-23)`. Связи: все `02-functional/*`, `04-architecture/03-database-schema.md`, `04-architecture/01-c4-context-containers.md`.

> Решение frozen: админка отдельным портом `admin-api :8081` (internal-only, не торчит наружу). Да, это безопаснее при условиях ниже.

## Что настраивается (без деплоя)
| Группа | Поля | Дефолт |
|---|---|---|
| Лимиты | `free.daily_limit`, `love.free_weekly`, `history.free_limit` | 1, 1, 20 |
| Тарифы | `plans[] {code, price_rub, stars_amount, duration_days, is_active}` (версионирование `UNIQUE(code,valid_from)`, в платежах снапшот) | 299/30, 2490/365, 99/single |
| Расклады | `spreads[] {is_active, sort_order, is_premium, config_json}` | см. каталог |
| Paywall-тексты | `copy.paywall_title/desc/cta` | «Продолжить безлимитно» |
| Trial | `trial.{enabled,days:3,require_tg:true}` | 3 дня за TG-Link, 1 раз |
| Рефералка | `referral.bonus_days, monthly_cap` | 3, 30 |
| AI | `ai.model, max_tokens, temperature, timeout_s` | см. `06-ai-pipeline` |

## Где хранится
- PG таблицы `plans`, `spreads`, `app_config(key,value,updated_at)` — источник правды.
- Redis-кэш 5 мин, инвалидация по `POST /v1/admin/config/publish` (только через `admin-api :8081`): точечно `DEL spreads:list:v1 plans:active:v1` (<5с; без Publish ≤5 мин).
- Доступ: только активная запись `admin_accounts` с `role=admin`, bcrypt-хэш пароля и `taro_admin` JWT; audit-log в `admin_audit`.
- Сеть: `admin-api :8081` слушает только `127.0.0.1`; наружу не публикуется, никакого IP-whitelist наружу. Доступ — только SSH-туннель `ssh -L 8081:127.0.0.1:8081 vps`. Статическая панель и API живут на одном loopback-порту; `ADMIN_API_TOKEN` используется только server-to-client вызовами.

## User Story
Как владелец, я хочу поменять цену с 299 на 349 и выключить Кельтский крест на выходные, чтобы проверить конверсию без программиста.

## GWT
- Given меняю `price_rub`, When Publish, Then новые оплаты по новой цене, старые подписки не трогаем; Redis инвалидирован <5с.
- Given выключаю spread, When GET spreads, Then его нет через ≤5 мин.

## DoD ротации расклада
Добавить новый расклад = 1 строка в `spreads` + позиции, без кода и деплоя. Если нужен код — это баг архитектуры.
