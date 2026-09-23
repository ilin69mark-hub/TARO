# Анимации (Framer Motion)

> Статус: `frozen v1.0 + Ultra + Premium`. Связи: `08-ultra-cinematic.md`, `09-premium-temple.md`.

| Анимация | Параметры |
|---|---|
| Вытягивание (рубашка из колоды) | `y: 40→0, opacity, duration .5, ease [0.22,1,0.36,1]`, stagger .08 |
| Переворот | `rotateY 0→180, duration .7, ease easeInOut`, backface hidden; `reduced-motion` → fade |
| Появление толкования | stream по токенам + `fade y 8px, duration .3`; caret `violet` blink |
| Paywall sheet | `spring damping 28 stiffness 300` |
| Тап | `whileTap scale .97` |
| Частицы (звезды) | canvas ≤40 частиц, только на результате, off при low-battery/reduced-motion |
| Ultra: letterbox-интро | полосы 8vh сверху/снизу, въезд .6с, skippable тапом, `ease [0.22,1,0.36,1]`; reduced-motion → off |
| Ultra: камера R3F | dolly 4.5→3.2, fov 45, `maath damp .8с`, pointer-параллакс ×0.03 desktop; gyro только desktop pointer:fine + тогл |
| Ultra: foil-переворот | `rotateY 0→180 .7с` + chromaticAberration .0012 (300мс) + вспышка пыли (12 спрайтов) |
| Premium акт 1 Arrival | камера (0,2.2,4.5)→(0,1.6,3.2) 2с, `maath damp .8`, fov 45 |
| Premium акт 2 Fan | веер 120°, stagger .08, tilt ±10° за pointer |
| Premium акт 3 Reading | топ-даун 15° (0,3.0,2.2) + shake .02 (300мс) на перевороте |

Бюджет: ≤2 одновременные layout-анимации, `transform/opacity` только (без layout thrash). Ultra-слои — только `transform/opacity/shader-time`, см. гейты в `08-ultra-cinematic.md`.
