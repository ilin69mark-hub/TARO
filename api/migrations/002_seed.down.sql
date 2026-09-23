-- 002_seed.down.sql — откат сидов T04 (структура остается)
DELETE FROM cards;
DELETE FROM spreads;
DELETE FROM plans;
DELETE FROM app_config;
DELETE FROM users WHERE id = '00000000-0000-0000-0000-000000000001';
