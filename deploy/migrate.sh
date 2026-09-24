#!/bin/sh
# migrate.sh — версионные миграции через golang-migrate (см. T30).
# Ручные psql -f приводили к дублям сидов (см. T18) — только этот скрипт.
# Использование: DATABASE_URL=postgres://... ./deploy/migrate.sh up|down [n]
set -eu
DIR="$(dirname "$0")/../api/migrations"
NET="${COMPOSE_NETWORK:-taro_internal}"

usage() { echo "usage: $0 up|down [steps]" >&2; exit 1; }
[ $# -ge 1 ] || usage

# migrate контейнер в той же сети, что db (доступ по имени db:5432)
# NB: DATABASE_URL передаём значением в аргументе — он виден в `ps` на время прогона.
# Для параноиков: DATABASE_URL_FILE=/run/secrets/db_url (файл 600) вместо env.
if [ -n "${DATABASE_URL_FILE:-}" ]; then
  DATABASE_URL="$(cat "$DATABASE_URL_FILE")"
  export DATABASE_URL
fi
if [ $# -ge 2 ]; then
  exec docker run --rm --network "$NET" \
    -v "$DIR:/migrations" \
    migrate/migrate:v4.18.2 \
    -path /migrations -database "${DATABASE_URL:?задай DATABASE_URL}" "$1" "$2"
else
  exec docker run --rm --network "$NET" \
    -v "$DIR:/migrations" \
    migrate/migrate:v4.18.2 \
    -path /migrations -database "${DATABASE_URL:?задай DATABASE_URL}" "$1"
fi
