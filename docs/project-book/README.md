# Книга разработки «Онлайн Таро» — Project Development Book

> Внутренняя библия solo-founder. Любое новое решение сверяется с этим индексом.
> Статус книги: `frozen v1.0 + Premium (2026-09-23)` — trial 3д ДА, single_99 в MVP ДА, admin-api :8081 internal-only, Premium-храм A + AI без ретуши + сдвиг MVP +10 дней.

**Стек (фиксирован, не менять без ADR):** Go · Next.js App Router + TS · Tailwind · Framer Motion · three/R3F Ultra · PostgreSQL · Redis · OpenRouter · Docker + GH Actions на VPS · Auth: Telegram ID + localStorage UUID.

**Два сквозных принципа (из твоих правок):**
1. `EntitlementsConfig` — тарифы, лимиты free, цены, тексты paywall настраиваются в админ-кабинете, а не хардкодятся.
2. `SpreadRegistry` — расклады ротируются (вкл/выкл, порядок, `is_premium`) без деплоя.

## Как пользоваться
1. 1 раздел = 1 папка, 1 подраздел = 1 файл. Простыни запрещены (>300 строк — дробить).
2. Каждый файл: `Статус` + `Связи` + заголовки H1–H3 + таблицы где уместно.
3. Порядок чтения новичку: `00-meta` → `01-manifest` → `02-functional` → `04-architecture`.
4. Изменение скоупа MVP — только через `06-rules/03-dod-frozen-scope.md`.

## Карта книги

### 00-meta
- [conventions](00-meta/conventions.md) — именование, статусы, шаблон файла

### 01-manifest — Манифест проекта
- [01-vision-mission](01-manifest/01-vision-mission.md) — Vision + Миссия
- [02-audience-personas](01-manifest/02-audience-personas.md) — 3 портрета
- [03-usp-competitors](01-manifest/03-usp-competitors.md) — отличие от Tarotoo/Arcana
- [04-principles](01-manifest/04-principles.md) — 5–7 принципов
- [05-antiscope](01-manifest/05-antiscope.md) — что НЕ делаем

### 02-functional — Функциональные требования
- [01-moscow-map](02-functional/01-moscow-map.md) — Must/Should/Could/Won't
- [02-auth](02-functional/02-auth.md) — Telegram ID + UUID
- [03-reading-flow](02-functional/03-reading-flow.md) — флоу расклада, GWT
- [04-spreads-catalog](02-functional/04-spreads-catalog.md) — 5 раскладов + ротация
- [05-entitlements-billing](02-functional/05-entitlements-billing.md) — free vs paid, тарифы как конфиг
- [06-referral](02-functional/06-referral.md) — рефералка
- [07-admin-config](02-functional/07-admin-config.md) — админ-кабинет настроек

### 03-nonfunctional — Нефункциональные требования
- [01-performance](03-nonfunctional/01-performance.md)
- [02-security-antifraud](03-nonfunctional/02-security-antifraud.md)
- [03-scalability-100-10k](03-nonfunctional/03-scalability-100-10k.md)
- [04-a11y](03-nonfunctional/04-a11y.md)
- [05-seo](03-nonfunctional/05-seo.md)
- [06-mobile-pwa](03-nonfunctional/06-mobile-pwa.md)
- [07-i18n-ru-future](03-nonfunctional/07-i18n-ru-future.md)

### 04-architecture — Архитектура
- [01-c4-context-containers](04-architecture/01-c4-context-containers.md)
- [02-interaction-next-go](04-architecture/02-interaction-next-go.md)
- [03-database-schema](04-architecture/03-database-schema.md) — + plans/entitlements/spreads
- [04-api-spec](04-architecture/04-api-spec.md)
- [05-cache-redis](04-architecture/05-cache-redis.md)
- [06-ai-pipeline](04-architecture/06-ai-pipeline.md)
- [07-payments](04-architecture/07-payments.md) — Stars №1
- [08-analytics-spec](04-architecture/08-analytics-spec.md) — события, PostHog, PG-бэкап (T07)

### 05-design — Дизайн-система
- [01-direction-dark-magic](05-design/01-direction-dark-magic.md)
- [02-palette](05-design/02-palette.md)
- [03-typography](05-design/03-typography.md)
- [04-components](05-design/04-components.md)
- [05-animations](05-design/05-animations.md)
- [06-glass-gradients](05-design/06-glass-gradients.md)
- [08-ultra-cinematic](05-design/08-ultra-cinematic.md) — Ultra-шейдеры + AI-арт 78 карт (frozen, база)
- [09-premium-temple](05-design/09-premium-temple.md) — 3D Люкс-храм, свет, пыль, камера (frozen)
- [07-tone-of-voice](05-design/07-tone-of-voice.md)

### 06-rules — Правила разработки
- [01-git-conventional](06-rules/01-git-conventional.md)
- [02-code-style-naming](06-rules/02-code-style-naming.md)
- [03-dod-frozen-scope](06-rules/03-dod-frozen-scope.md)
- [04-ai-code-review-logs](06-rules/04-ai-code-review-logs.md)

### 07-roadmap — Роадмап
- [01-week1-4-mvp](07-roadmap/01-week1-4-mvp.md)
- [02-month2-3-analytics-bot](07-roadmap/02-month2-3-analytics-bot.md)
- [03-month4-6-native-criteria](07-roadmap/03-month4-6-native-criteria.md)
- [04-kpi](07-roadmap/04-kpi.md)

### 08-risks — Риски
- [01-risk-table](08-risks/01-risk-table.md)
- [02-legal-disclaimer](08-risks/02-legal-disclaimer.md)

### 09-adr — Журнал решений
- [00-template](09-adr/00-template.md)
- [01-go-vs-node](09-adr/01-go-vs-node.md)
- [02-next-vs-sveltekit](09-adr/02-next-vs-sveltekit.md)
- [03-openrouter-vs-openai](09-adr/03-openrouter-vs-openai.md)
- [04-telegram-vs-email](09-adr/04-telegram-vs-email.md)
- [05-pwa-vs-native](09-adr/05-pwa-vs-native.md)
- [06-admin-configurable-entitlements](09-adr/06-admin-configurable-entitlements.md)
- [07-premium-scope](09-adr/07-premium-scope.md) — люкс-храм A + сдвиг (frozen)
- [08-lcp-budget](09-adr/08-lcp-budget.md) — split LCP (frozen)

### 10-appendices — Приложения
- [01-glossary](10-appendices/01-glossary.md)
- [02-links](10-appendices/02-links.md)
- [03-predeploy-checklist](10-appendices/03-predeploy-checklist.md)
- [04-ads-launch-checklist](10-appendices/04-ads-launch-checklist.md)

## Прогресс
- [x] Батч 0 — скелет
- [x] Батч 1 — manifest
- [x] Батч 2 — functional
- [x] Батч 3 — architecture
- [x] Батч 4 — NFR + design
- [x] Батч 5 — rules/roadmap/risks/ADR/appendices → `frozen v1.0 (2026-09-23)`
- [x] Аудит-фикс A (биллинг) + B (архитектура) + C (храм/a11y/KPI/юр/ADR-07/08) — 2026-09-23
