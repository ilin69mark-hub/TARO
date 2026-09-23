# Glass + градиенты

> Статус: `frozen v1.0 + Ultra`. Связи: `08-ultra-cinematic.md`.
- Glass: `bg #151524/60 + backdrop-blur-16 + border white/8 + inner gold/10`. Только на sheet/tabbar/result-card, не на весь экран (perf).
- Градиенты: `gold: linear(135deg #D4AF37→#F1D97B)`, `night: radial violet/20 → transparent 70%` за колодой, `text-glow: 0 0 24px gold/30`.
- Правило: max 1 glow + 1 gradient на вьюпорт. Никаких анимированных градиентов на фоне (батарея).
- Ultra-исключение (frozen): анимированный фон разрешен только как 1 GLSL-quad + пост (bloom/vignette/grain/CA) по цепочке из `08-ultra-cinematic.md`; на слабых — автогораздо в Lite.
