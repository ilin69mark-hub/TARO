# Каталог раскладов (5 + ротация)

> Статус: `draft`. Связи: `07-admin-config.md`, `04-architecture/03-database-schema.md` (`spreads`).

## Дефолтные 5 (подтверждены)
| # | Код | Название | Позиции | Доступ | Описание |
|---|---|---|---|---|---|
| 1 | `daily` | Карта дня | 1 | free | быстрый ритуал, хук возврата |
| 2 | `three` | Прошлое–Настоящее–Будущее | 3 | free (в лимит) | универсальный |
| 3 | `love` | Отношения | 3 (ты/он/между) | 1 free/нед, иначе premium | для Алины |
| 4 | `decision` | Решение (5 карт) | 5 (суть/за/против/риск/совет) | premium | для Марины |
| 5 | `celtic` | Кельтский крест | 10 | premium | для Дарьи, глубина |

Каждый: `name`, `description`, `positions[{index,label,meaning}]`, `is_premium`, `is_active`, `sort_order`, `config_json {reversed_chance:0.15}`.

## Ротация без деплоя (твое требование)
- Админка: вкл/выкл `is_active`, порядок `sort_order`, перевод в premium, сезонные (ex. `fullmoon` — создать за 5 мин копией, включить на 3 дня).
- API `GET /v1/spreads` отдает только `is_active=true`, кэш Redis 5 мин (`spreads:list:v1`).
- Удалять нельзя, только деактивировать (история чтений ссылается).
- E18-решение (frozen 2026-09-23): `newyear` — окно 25.12–14.01 через `spreads.seasonal` + cron;
  `fullmoon` — окна ±3 дня от полнолуния проставляются вручную раз в месяц (5 мин в админке:
  Publish нового окна). Автолунный календарь — не делаем (overengineering для 3 дней).

## Acceptance
- Given `celtic.is_active=false`, When GET spreads, Then его нет, старые readings открываются.
- Given free юзер открывает premium, When тап, Then paywall с ценой из `plans` (не хардкод).
