# Рефералка v1 (Should, но проектируем сейчас)

> Статус: `draft`. Связи: `05-entitlements-billing.md`.

## Механика (простая, антифрод-устойчивая)
- Код `taro.me/r/<ref_code>` (`referrals.code UNIQUE`, генерация при первом `GET /v1/referral/me`). Флоу: `POST /v1/referral/apply {code}` до 1-го reading → `pending`; хук после 1-го reading → проверка → `completed`.
- Бонус +3 дня premium обоим (конфиг `referral.bonus_days=3`) начисляется в `subscriptions` (не в `entitlements`), только если `referee.tg_id IS NOT NULL` (anon-ферма отрезана) + `referrer != referee` + fingerprint/IP mismatch.
- Лимиты: max 30 дней/календарный месяц (`entitlements.referral_bonus_month` + ключ `YYYY-MM`) + lifetime-счетчик `referral_bonus_lifetime`. Второй код на того же referee → 409 ALREADY_REFERRED (не 500).

## User Story
Как пользователь, я хочу поделиться красивым раскладом с подругой, чтобы мы обе получили бонус.

## GWT
- Given `apply` до reading + referee привязал TG + 1 reading, When хук, Then оба `subscriptions.valid_until = max(now,valid)+3д`, `referrals completed`.
- Given сам себе по своей ссылке (тот же fingerprint) или anon без TG, When хук, Then бонуса нет, `referrals status=rejected`.

## Таблица `referrals`
`id, referrer_id, referee_id, code, status(pending/completed/rejected), bonus_days, created_at` — уникальный `referee_id`; код юзера — `users.referral_code` (self-строки запрещены с миграции 014: ломали apply).
