# Performance

> Статус: `frozen v1.0 + Ultra + Premium (2026-09-23)`. Связи: `04-architecture/02-interaction-next-go.md`, `05-design/08-ultra-cinematic.md`, `05-design/09-premium-temple.md`.

| Метрика | Цель | Как меряем |
|---|---|---|
| LCP лендинга (без 3D) | <2.5s mobile 4G (fail в CI) | Lighthouse CI fail если >2.8s |
| LCP расклада/результата с 3D | desktop <4.0s / mobile 4G <5.0s (warn; fail если mobile >5s) | Lighthouse + первый paint CTA/текста без ожидания 3D; интро не блокирует LCP, autoplay 3D запрещен до LCP |
| FPS сцена результата | ≥45 desktop / ≥30 mobile | R3F `perf` + эталоны: iPhone 12 (Safari) + Moto G54/Pixel 6a (Chrome 4G) + скрин в PR |
| FPS храм Premium | цель 60/≥30, минимум 50/28 с даунгрейдом | гард `<28fps 1.5с → даунгрейд, <22fps → Lite` |
| 3D-бандл | lazy `ssr:false`, первый paint без 3D | `bundle-analyzer`, 3D чанк отдельно |
| JS-бандл (без 3D) | <180KB gzip | бюджет в CI остается |
| Images карт | WebP 512px <90KB/шт + blur placeholder, lazy | `next/image` |
| Видео-петли | WebM <800KB, только desktop, `preload=none` | audit размера |

Правила: SSR для лендинга/SEO, CSR + SSE для расклада; `next/font` self-host; Redis обязателен для spreads/plans; AI-кэш 7 дней режет 30% вызовов.
