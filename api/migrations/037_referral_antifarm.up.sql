-- 037: антиферма рефералки — снапшот личности при apply + причина отказа.
--
-- Проблемы, которые закрывает (аудит рефералки, P1–P8):
--   1) fingerprint/IP-фильтр был ЗАДЕКЛАРИРОН в docs/project-book/02-functional/06,
--      но в коде отсутствовал (0 совпадений в referral.go). Единственная защита
--      от фермы была «у рефере есть tg_id» = 30 дней/месяц на аккаунт, что и
--      было целью атаки: 10 tg-аккаунтов с одним клиентским fingerprint давали
--      month=30, 10 бонусных подписок.
--   2) Антиферма проверялась ТОЛЬКО в момент хука (по tg_id рефера). Пока
--      referee аноним, строка pending висела без единой проверки и при merge
--      аккаунтов переезжала на tg-юзера, где и завершалась.
--   3) Не было видно, ПОЧЕМУ отказ. Из-за этого нельзя было отличить «anon без
--      TG» (легитимно переиграть после входа) от «антиферма» (нельзя).
--
-- Что делаем:
--   1) refs-сигналы (fingerprint + IP обеих сторон) снимаются в МОМЕНТ apply и
--      пишутся в referrals. Проверка при complete сверяет их с текущим
--      состоянием пользователей, поэтому подмена личности через merge и смена
--      fingerprint ловятся на завершении, а не только на входе.
--   2) applied_code — фактически применённый код (аудит). До этой миграции в
--      referrals.code писался СЛУЧАЙНЫЙ genCode(), и эта колонка не читалась ни
--      одним запросом: расследовать «какой код применил юзер» было нечем.
--      referrals.code остаётся как legacy-токен строки (UNIQUE), т.к. одним
--      кодом пользуются многие рефереи и UNIQUE на нём невозможен.
--   3) reject_reason — причина отказа. Разрешает переиграть только 'anon'.
--   4) users.referral_ip — IP юзера в момент, когда он забрал/показал свой код
--      (GET /me). Это «откуда инвайт используется», доступно без новых таблиц.
--   5) Частичный индекс на pending — для reconciler'а (#7: подвисшие pending
--      после рестарта/таймаута больше не остаются навсегда).
--
-- Исторические строки получают пустые снапшоты и NULL-причину: на старых
-- completed-строках это ничего не меняет, а pending старше миграции
-- отсеются reconciler'ом по отсутствию сигналов (см. referral.go).

ALTER TABLE referrals
  ADD COLUMN IF NOT EXISTS applied_code TEXT NULL,
  ADD COLUMN IF NOT EXISTS referee_fp   TEXT NULL,
  ADD COLUMN IF NOT EXISTS referee_ip   TEXT NULL,
  ADD COLUMN IF NOT EXISTS referrer_fp  TEXT NULL,
  ADD COLUMN IF NOT EXISTS referrer_ip  TEXT NULL,
  ADD COLUMN IF NOT EXISTS reject_reason TEXT NULL;

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS referral_ip TEXT NULL;

-- reconciler: pending старше порога + индекс под него.
CREATE INDEX IF NOT EXISTS idx_referrals_pending ON referrals (created_at)
  WHERE status = 'pending';

-- Конфиг рефералки наконец начал читаться кодом (раньше 'referral' в
-- app_config был мёртвой записью: bonus_days/monthly_cap были захардкожены).
-- Добавляем ТОЛЬКО новые ключи, через '||' — существующие значения не
-- затираются, если админ уже менял monthly_cap.
--
-- lifetime_cap_days = 300 — потолок на всю жизнь (100 рефералов). Ставим
-- конечным: до этой миграции потолка не было вовсе, и месячный кэп был
-- единственным ограничителем, что и делало его целью атаки.
-- antifarm_fp/antifarm_ip — аварийный тормоз: у мобильных операторов CGNAT
-- даёт честным пользователям общий IP, и владелец должен суметь ослабить
-- проверку без правки кода.
-- fingerprint_cluster_limit=5 — порог «один браузер = много аккаунтов».
-- Нужен помимо парных проверок: вектор «чужой реферальный код + 10 своих
-- аккаунтов из одного браузера» парными проверками не ловится (fingerprint
-- реферера чужой, совпадения нет), а бонусы получает атакующий. 5 — с запасом
-- для семьи за одним ноутбуком.
-- ip_cluster_limit=0 — выключен: CGNAT делает подсчёт рефёров по адресу
-- источником ложных отказов. Включать осознанно и с пониманием цены.
INSERT INTO app_config (key, value) VALUES
  ('referral', '{"lifetime_cap_days":300,"antifarm_fp":true,"antifarm_ip":true,"fingerprint_cluster_limit":5,"ip_cluster_limit":0}'::jsonb)
ON CONFLICT (key) DO UPDATE SET value = app_config.value || EXCLUDED.value;
