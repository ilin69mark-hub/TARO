# Premium Temple — 3D Люкс-храм (frozen 2026-09-23)

> Статус: `frozen v1.0 + Premium`. Связи: `08-ultra-cinematic.md` (база), `05-animations.md`, `03-nonfunctional/01-performance.md`, `07-roadmap/01-week1-4-mvp.md`.
> Выбор: A (3D Люкс-храм) + AI без доработки + сдвиг MVP +10 дней ради качества.

## 1. Сцена храма (procedural, 0 внешних моделей)
- Пол: desktop — `MeshReflectorMaterial` (blur 300, mixStrength 12, color `#0B0B14`); mobile — запрещено (дорого), замена `gradient fake-plane` (статика). Туман над полом (2-й quad, opacity .25).
- 6 колонн-силуэтов: `CylinderGeometry` low-poly + `MeshStandardMaterial` `#151524`, rim-подсветка violet; купол-свечение: большой `Sprite` radial gold 8%.
- Всё procedural — ноль скачиваний моделей, весь храм <150 строк R3F. Версии пинами (см. `08-ultra-cinematic.md`).

## 2. Свет (кино, 3 источника)
| Источник | Тип / цвет | Параметры |
|---|---|---|
| Key gold | `spotLight #F1D97B` сверху-сбоку | angle .5, penumbra 1, intensity 60, castShadow desktop only (mobile — без теней), shadow 1024 PCFSoft |
| Rim violet | `directionalLight #7C5CFF` сзади | intensity 2.2, без теней |
| Candle | `pointLight #D4AF37` у колоды | intensity 8 static (flicker запрещен как >3 вспышек/с по WCAG 2.3.1; допуск ≤2Гц ±5% только desktop без reduced-motion) |

God-rays: 2 additive planes за колодой, opacity .12, depthWrite false. Тонемаппинг `ACESFilmic`, exposure 1.1, warm LUT (тени в gold).

## 3. Пыль GPU (600 desktop / ≤150 mobile / 0 Lite)
`Points` + custom shader: дрейф вверх .05/с, мерцание sin(time*2+seed), размер 1–3px по дистанции, additive. Пауза когда таб скрыт (`visibilitychange`). Первой выключается при FPS-гарде. Mobile >150 запрещено.

## 4. Карты Premium (без ретуши арта)
- Edge-gilding: кромка через `onBeforeCompile` — emissive gold на grazing angle; толщина extrude 0.02 (чувствуется вес).
- Тиснение: отдельный grayscale bump (не blur-placeholder — в 20px-блуре нет деталей, будет грязь) или убрать; `bumpScale .015`.
- Арт как есть: единый апскейл + LUT-грейдинг кодом; брак → перегенерация, не Photoshop. База: 22 старших + шаблон младших (масть/номер оверлеем: шрифт Cormorant 48px, позиция низ-центр); домасть 78 — Нед.6.
- Крупные планы запрещены кодом: `assert camera.distance >= 2.2` + скрин-ревью 78 карт на 27" в PR.

## 5. Хореография камеры (3 акта, `maath`)
1. Arrival 2с: pos (0,2.2,4.5) → (0,1.6,3.2), fov 45, damp .8.
2. Fan: веер карт дугой 120°, stagger .08, tilt следует за pointer ±10°.
3. Reading: топ-даун 15° (0,3.0,2.2), shake 0.02 на перевороте 300мс + CA .0012.
Всё skippable тапом; gyro/touch-параллакс ×0.03 только desktop `pointer:fine` + явный тогл (iOS требует `DeviceOrientation` permission — флоу в `08-ultra-cinematic.md`); на mobile — touch-параллакс ×0.015 или off.

## 6. Звук
Drone `opus/mp3 dual` <300KB, выкл дефолт, fade по актам gain (0→.2→.1, max .2), duck при VoiceOver/aria-live. Никакого автоплея.

## 7. Даунгрейд-лестница (обязательна)
`reflector+bloom off → тени off → пыль off → grain off → Lite-статика`. Триггер: `<28fps 1.5с → даунгрейд, <22fps → Lite` или `deviceMemory<4` или `reduced-motion` или ручной «Спокойный режим» в /profile (persisted). Эталоны: iPhone 12 (Safari) + Moto G54 / Pixel 6a (Chrome 4G); R3F perf overlay скрин в PR.

## 8. DoD храма
- [ ] Результат ≥45/≥30, храм цель 60/≥30 (минимум 50/28 с даунгрейдом), замер на эталонах выше
- [ ] Версии из `08-ultra` пинами + lockfile, крупных планов нет (assert ≥2.2м), LUT един
- [ ] Canvas `aria-hidden=true` + DOM-дубль выбора (радиогруппа/кнопки) с фокусом; tilt — только enhancement
