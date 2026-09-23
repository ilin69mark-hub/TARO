# Ultra Cinematic (уровень C — решение frozen 2026-09-23)

> Статус: `frozen v1.0` (база для Premium). Связи: `01-direction-dark-magic.md`, `05-animations.md`, `06-glass-gradients.md`, `03-nonfunctional/01-performance.md`, `09-premium-temple.md` (надстройка).
> Решение: Ultra-шейдеры + AI-генерация 78 карт + «красота любой ценой» (LCP-бюджет ослаблен осознанно, детали в ADR-08). Premium-храм — в `09-premium-temple.md`, не дублировать.

## 1. Сцена (что видит юзер)
Letterbox-интро 1.2с (skippable) → туман-шейдер расходится от точки тапа → камера R3F наезжает на колоду → веер карт с foil → переворот с chromatic aberration + пыль → толкование поверх зерна/виньетки.

## 2. Стек (пины, проверить peerDeps; fiber@8 = React 18 + Next 14)
- `three@0.169.0`, `@react-three/fiber@8.17.10`, `@react-three/drei@9.114.3`, `@react-three/postprocessing@2.16.3`, `maath@0.10.8`, `lenis@1.1.14`, `framer-motion@11.11.9` (+ `react@18.3.1`, `next@14.2.x`).
- Фон: 1 fullscreen quad, GLSL nebula/fog (fbm 3 октавы, violet `#7C5CFF` 20% + gold `#D4AF37` 8%), параллакс pointer ×0.03 desktop; gyro только desktop `pointer:fine` + тогл (iOS `DeviceOrientation.requestPermission()` флоу: кнопка «Включить глубину» → permission → gyro, иначе touch).
- Карты: `meshPhysicalMaterial` + кастомный foil-shader (fresnel + value-noise перелив), tilt ±10° от pointer, `roughness .35 metalness .1`.
- Петли фона (только desktop, `matchMedia pointer:fine`): 2 WebM 5с loop <800KB (туман/звезды), `preload=none`, старт после `onload`.
- Звук: выкл по дефолту, drone mp3 <300KB по тоглу.

## 3. Пост-эффекты (порядок)
`Bloom (intensity .6, luminanceThreshold .75) → ChromaticAberration (offset .0012 только на перевороте 300мс) → Noise (opacity .08) → Vignette (.35)` + `text-glow 0 0 24px gold/30`.

## 4. AI-арт 78 карт (пайплайн)
- Ядро промпта: `dark temple gold-foil tarot emblem, deep violet night #0B0B14, antique gold #D4AF37, Art Nouveau + A24 film grain, centered symmetric emblem, no text, stylized occult figures faces distant/averted hands hidden/simple` + фикс seed + 1 якорь (Маг). Брак-чеклист: руки/лица/текст/асимметрия/уплывшая палитра.
- Батчи по 10, отбор вручную (заложить дни в роадмап); файлы `public/cards/major-00-fool.webp` (512px, <90KB) + blur 20px placeholder; reversed — тот же арт, флип кодом. Лицензия генератора + тариф с commercial use + дата — в `10-appendices/02-links.md`.
- База: сначала 22 старших + 1 шаблон младших (масть/номер оверлеем), потом домасть 78 (Нед.6).
- Бюджет: ~250 генераций (брак ×3), $10–20 разово.

## 5. Гейты (чтобы не сжечь телефоны)
- `deviceMemory<4` или `hardwareConcurrency<=4` или `reduced-motion` или «Спокойный режим» → Lite: статика + CSS-туман, без R3F/post.
- R3F только `dynamic import ssr:false` за `Suspense`; первый paint — CTA/текст без ожидания 3D (LCP мерим по нему).
- FPS-гард: `<28fps 1.5с → даунгрейд (reflector+bloom → тени → пыль → grain), <22fps → Lite`.
- SW-кэш: только 22 majors + шаблон + shell (остальные 56 lazy on-demand); 3D-чанк CacheFirst только desktop/wifi, mobile — NetworkFirst/Lite. CDN Cloudflare free.

## 6. DoD Ultra-экрана
- [ ] Результат ≥45/≥30, храм 60/≥30 (мин 50/28), замер на iPhone 12 + Moto G54/Pixel 6a + R3F perf скрин в PR
- [ ] Интро skippable, звук выкл дефолт, reduced-motion → Lite-статика (маппинг в `03-nonfunctional/04-a11y.md`), «Спокойный режим» в /profile
- [ ] Арты 22+шаблон (потом 78) в WebP + blur, лицензия в `10-appendices/02-links.md`
