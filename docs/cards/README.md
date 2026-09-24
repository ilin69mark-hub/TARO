# Промпт-пак: 22 старших аркана (E13)

> Статус: `инструкция владельцу`. Связи: `05-design/08-ultra-cinematic.md`, `../public/cards/README.md`.
> Лицензия: /// ⛔ ВПИСАТЬ (см. ../OWNER_TODO.md) ///

## Фикс-ядро (копипаст в КАЖДУЮ генерацию, без изменений)
```
dark temple gold-foil tarot emblem, deep violet night background #0B0B14,
antique gold ornaments #D4AF37, Art Nouveau borders, A24 film grain,
centered symmetric emblem composition, no text, no letters, no watermark,
stylized occult figures with faces distant or averted, hands hidden or simple gestures,
soft candle glow, museum quality, ultra detailed
```
Негативный промпт (всегда):
```
photorealistic face, close-up face, deformed hands, extra fingers, text,
letters, watermark, logo, blurry, low quality, neon cyberpunk, cartoon
```

## Seed-процедура
1. Сгенерируй Мага (01) первым — это ЯКОРЬ стиля.
2. Зафиксируй seed якоря, используй его для всех 21 остальных.
3. Батчи по 10: 00–09, 10–19, 20–21 + повторы брака.

## Брак-чеклист каждой карты
- [ ] Руки: нет лишних пальцев/деформаций
- [ ] Лица: вдаль или отвернуты, не фотореализм
- [ ] Текст: ни одной буквы/цифры
- [ ] Палитра: ночь #0B0B14 + золото #D4AF37, без неона
- [ ] Симметрия эмблемы по центру

## Техблок (все файлы) — РАЗМЕРЫ frozen
- Формат: портрет **5:8** (совпадает с UI `aspect 5/8` в `CardArt` — без кропа!).
- Исходник генерации: **1000×1600 px**.
- Доставка: даунскейл **500×800 px**, WebP q80, **<90KB** каждый.
- Blur-плейсхолдер 20px (генерит код).
- Печать (если понадобится): 70×112мм, 300dpi из исходника.
- Имя = image_key из сида (major-00-fool.webp … major-21-world.webp).
- Класть в `web/public/cards/`, контракт в `web/public/cards/README.md`.

## Файлы
- `major-00-fool.md` … `major-21-world.md` — сцена + акценты + ловушки брака
- Младшие 56 — ВТОРЫМ заходом (шаблон 4 мастей + оверлей номера Cormorant)
