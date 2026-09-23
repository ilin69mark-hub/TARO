# MoSCoW-карта фич

> Статус: `draft`. Связи: `01-manifest/05-antiscope.md`, все файлы `02-functional/`.

## Must (MVP, неделя 1–4)
| Фича | Файл |
|---|---|
| Auth TG + UUID anon | `02-auth.md` |
| Флоу расклада + AI-толкование | `03-reading-flow.md` |
| 5 раскладов (реестр) | `04-spreads-catalog.md` |
| Free vs Paid + paywall | `05-entitlements-billing.md` |
| Оплата Telegram Stars | `04-architecture/07-payments.md` |
| История раскладов (последние 20) | `03-reading-flow.md` |
| Админ-конфиг тарифов/раскладов | `07-admin-config.md` |
| Trial 3д за TG-Link + single_99 (Must, frozen) | `05-entitlements-billing.md` |
| Ultra-шейдеры база + Premium-храм A (Must, сдвиг +10д) | `05-design/08-ultra-cinematic.md`, `09-premium-temple.md` |
| PWA-install, дисклеймер | `03-nonfunctional/06-mobile-pwa.md` |

## Should (мес. 2–3, если метрики ок)
- Рефералка v1 (промо-дни за друга) — `06-referral.md`
- Telegram-бот-напоминалка «карта дня»
- Поделиться раскладом (картинка OG)
- Кэширование AI по паре (расклад+карты) в Redis

## Could (мес. 4–6)
- Дневник + вопросы для рефлексии с сохранением
- Ротация сезонных раскладов (Полнолуние, Новый год) через `is_active`
- Boosty/ЮKassa как второй способ оплаты
- Web push-напоминания

## Won't (не в ближайшие 6 мес)
Живые тарологи, соцсеть, конструктор раскладов, 2-я колода, EN-локализация UI, натив сразу (только по критерию 1000 платных). В люксе НЕ делаем: ретушь арта вручную, внешние 3D-модели, видео-петли на mobile, автоплей звука.
