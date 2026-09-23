# Mobile-first + PWA

> Статус: `draft`. Связи: `05-design/04-components.md`.

- Дизайн от 360×640 вверх; таб-бар снизу (Главная/Расклады/История/Профиль).
- PWA: `manifest.json` (name, icons 192/512, theme `#0B0B14`), `service-worker`: кэш shell + 22 majors + шаблон младших + spreads 24ч; остальные 56 карт — lazy on-demand; 3D-чанк CacheFirst только desktop/wifi, mobile — NetworkFirst/Lite.
- Install-промпт после 2-го расклада (не раньше!). Offline: показываем закэшированные значения карт, AI — «нужен интернет».
- Install-промпт после 2-го расклада (не раньше!). Offline: показываем закэшированные значения карт, AI — «нужен интернет».
- Критерий: Lighthouse PWA 100, installable на Android + iOS (Add to Home).
