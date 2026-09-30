# web — Next.js PWA (см. docs/project-book/03-nonfunctional/06)
FROM node:20-alpine AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

FROM node:20-alpine AS build
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
# Аудит D: NEXT_PUBLIC_* только через build-args (.env в образ не едет, см. .dockerignore).
ARG NEXT_PUBLIC_BASE_URL=""
ARG NEXT_PUBLIC_POSTHOG_KEY=""
# Стенд сцены /design-lab. NEXT_PUBLIC_* встраивается на СБОРКЕ, поэтому флаг
# надо провести через build-args: переменная в docker-compose environment на
# готовый образ не действует, и стенд остаётся закрытым в любом случае.
ARG NEXT_PUBLIC_DESIGN_LAB="0"
# Ссылка на Telegram-приложение для приглашения привязки аккаунта
# (см. 02-functional/02). Пусто — UI покажет текст вместо ссылки.
ARG NEXT_PUBLIC_TG_APP_URL=""
ENV NEXT_PUBLIC_BASE_URL=$NEXT_PUBLIC_BASE_URL NEXT_PUBLIC_POSTHOG_KEY=$NEXT_PUBLIC_POSTHOG_KEY NEXT_PUBLIC_DESIGN_LAB=$NEXT_PUBLIC_DESIGN_LAB NEXT_PUBLIC_TG_APP_URL=$NEXT_PUBLIC_TG_APP_URL
RUN npm run build

FROM node:20-alpine AS run
WORKDIR /app
ENV NODE_ENV=production
COPY --from=build --chown=node:node /app/.next ./.next
COPY --from=build /app/node_modules ./node_modules
COPY --from=build /app/package.json ./package.json
COPY --from=build /app/public ./public
# S09: non-root (встроенный user node), healthcheck
# Каталог кэша next/image и fetch-cache должен принадлежать пользователю
# рантайма: иначе каждый оптимизированный запрос отдаёт 200 клиенту, но
# падает EACCES при записи (A03/F-03). Каталог мог не создаться на сборке.
RUN mkdir -p /app/.next/cache/images && chown -R node:node /app/.next
USER node
EXPOSE 3000
HEALTHCHECK --interval=30s --timeout=3s --retries=3 CMD wget -qO- http://127.0.0.1:3000/ || exit 1
CMD ["npm", "run", "start"]
