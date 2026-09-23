-- 011_seasonal_seed.up.sql — пак сезонных раскладов, inactive по умолчанию (см. V21).
-- Включение — авторотацией V22 или вручную через Publish.
INSERT INTO spreads (code, name_ru, positions, is_premium, is_active, sort_order, config_json) VALUES
  ('fullmoon', 'Полнолуние',
    '[{"index":0,"label":"Отпустить","meaning":"что оставить в тени"},{"index":1,"label":"Принять","meaning":"что признать"},{"index":2,"label":"Осветить","meaning":"что вывести на свет"}]',
    false, false, 60, '{"reversed_chance":0.15}'),
  ('newyear', 'Новый год',
    '[{"index":0,"label":"Уходящий","meaning":"что взять с собой"},{"index":1,"label":"Урок","meaning":"главный урок года"},{"index":2,"label":"Желание","meaning":"истинное желание"},{"index":3,"label":"Ресурс","meaning":"на что опереться"},{"index":4,"label":"Первый шаг","meaning":"с чего начать"}]',
    false, false, 70, '{"reversed_chance":0.15}');
