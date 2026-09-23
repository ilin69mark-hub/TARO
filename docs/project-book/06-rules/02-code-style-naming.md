# Code Style + именование

> Статус: `draft`.
- Go: `gofmt + go vet + golangci-lint`, пакеты `internal/auth`, ошибки `ErrLimitExceeded`, хендлеры `HandleCreateReading`, SQL через `sqlc`.
- TS: ESLint `next/core-web-vitals` + Prettier (2 spaces, singleQuote, 100 col). Компоненты `PascalCase.tsx` (`TarotCard.tsx`), хуки `useReading.ts`, страницы App Router `app/(tabs)/spreads/page.tsx`.
- API: `/v1 snake_case`, JSON `snake_case` (`spread_code`), даты ISO8601.
- ENV: `OPENROUTER_API_KEY`, `TG_BOT_TOKEN`, `JWT_SECRET` — только env, никогда в код.
