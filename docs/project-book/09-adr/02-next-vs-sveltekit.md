# ADR-02: Next.js, а не SvelteKit

> Статус: `frozen`.
## Контекст
Нужны SEO-лендинг + PWA + русское комьюнити для найма помощи.
## Варианты
- SvelteKit: меньше JS, но мало RU-примеров PWA/SEO, риск упереться в соло.
- Next.js App Router: SSR/ISR из коробки, `next/image/font`, PWA-плагины, Framer Motion зрелый.
## Решение
Next.js 14 + TS.
## Последствия
+ найм/помощь легко; − бандл больше (лечим бюджетом 180KB).
