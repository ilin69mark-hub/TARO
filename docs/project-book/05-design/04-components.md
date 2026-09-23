# Компоненты

> Статус: `draft`. Связи: `05-animations.md`.

- **Button primary:** `bg gold gradient → #D4AF37→#F1D97B`, текст `#0B0B14`, radius 16, h-12, press scale .97.
- **Button ghost:** бордер `1px gold/40`, текст gold.
- **TarotCard:** aspect 2/3.2, radius 20, рубашка (паттерн SVG + gold-рамка), лицо — WebP из `/cards`.
- **BottomSheet paywall:** snap 90%, drag-close, цена из `plans` (не хардкод!).
- **TabBar:** 4 иконки, active gold + dot, blur bg.
- **ReadingResult:** карта → толкование (streaming caret violet) → 2 вопроса рефлексии → CTA share/more.
- Экраны: `/` лендинг, `/spreads`, `/reading/[id]`, `/history`, `/profile` (лимиты + тарифы из entitlements).
