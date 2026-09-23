# i18n: только RU, закладка на будущее

> Статус: `draft`.
- На старте `next-intl` с одной локалью `ru`, все строки в `messages/ru.json` (не хардкод в JSX!).
- Ключи: `reading.daily.title`, `paywall.cta` — те же ключи позже лягут в `app_config` для админки.
- Форматы: `Intl.DateTimeFormat('ru-RU')`, валюта `RUB`. PG `text` в UTF-8, без enum на русском.
- EN — только после 1000 платных (см. роадмап), включением `en.json` без рефакторинга.
