# Контракт артов карт (T24)

Файлы лежат здесь: `public/cards/*.webp` (500×800 px, 5:8, WebP q80 <90KB).
Имя файла = `image_key` из таблицы `cards` (см. сид 002). Код подхватывает без изменений.

## Статус
- [ ] 22 старших аркана (major-00…major-21)
- [ ] 4 шаблона младших (по мастям, номер оверлеем Cormorant)
- [ ] Остальные 56 (домасть, нед.6)

Пока файлов нет — UI показывает fallback-рубашку (см. components/CardArt.tsx).

## Промпт-ядро (frozen, см. 05-design/08)
`dark temple gold-foil tarot emblem, deep violet night #0B0B14, antique gold #D4AF37, Art Nouveau + A24 film grain, centered symmetric emblem, no text, stylized occult figures faces distant/averted hands hidden/simple` + фикс seed + якорь Маг.

## Процедура
Батчи по 10, брак-чеклист: руки/лица/текст/асимметрия/уплывшая палитра. Брак → перегенерация, не ретушь.

## Лицензия (заполнить при генерации)
/// ⛔ ВПИСАТЬ ВЛАДЕЛЬЦУ (см. docs/OWNER_TODO.md) ///
- Генератор: …
- Тариф с commercial use: …
- Дата: …
