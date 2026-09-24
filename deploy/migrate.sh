#!/bin/sh
# migrate.sh — версионные миграции через golang-migrate (см. T30).
# Ручные psql -f приводили к дублям сидов (см. T18) — только этот скрипт.
# Использование: ./deploy/migrate.sh up|down [n]  (читает .env рядом с репо)
set -eu
DIR="$(dirname "$0")/../api/migrations"
NET="${COMPOSE_NETWORK:-taro_internal}"

usage() { echo "usage: $0 up|down [steps]" >&2; exit 1; }
[ $# -ge 1 ] || usage

# Аудит D: down на проде — только с ALLOW_DOWN=1 и подтверждением (риск потери данных).
if [ "$1" = "down" ] && [ "${ALLOW_DOWN:-0}" != "1" ]; then
  echo "ОТКАЗАНО: down требует ALLOW_DOWN=1 (см. аудит D, down-миграции 002/006/014/018/019)" >&2
  exit 1
fi
if [ "$1" = "down" ]; then
  printf 'Точно откатить миграции (%s)? Введи ДА: ' "$*"
  read -r ans
  [ "$ans" = "ДА" ] || { echo "отмена" >&2; exit 1; }
fi

# migrate контейнер в той же сети, что db (доступ по имени db:5432)
# Аудит D: пароль НИКОГДА не идёт argv/ps — только --env-file + sh-обёртка внутри.
# Источник: .env (600) рядом с репо или DATABASE_URL_FILE. Env напрямую НЕ поддерживается
# намеренно (значение светилось бы в `ps aux` хоста).
ENVFILE="$(dirname "$0")/../.env"
if [ -n "${DATABASE_URL_FILE:-}" ]; then
  printf 'DATABASE_URL=%s\n' "$(cat "$DATABASE_URL_FILE")" > "$ENVFILE.tmp.$$"
  ENVARG="--env-file $ENVFILE.tmp.$$"
  trap 'rm -f "$ENVFILE.tmp.$$"' EXIT INT TERM
elif [ -f "$ENVFILE" ]; then
  ENVARG="--env-file $ENVFILE"
else
  echo "Нет .env и DATABASE_URL_FILE — создай .env (600) с DATABASE_URL (см. .env.example)" >&2
  exit 1
fi
if [ $# -ge 2 ]; then
  # shellcheck disable=SC2086
  exec docker run --rm --network "$NET" \
    -v "$DIR:/migrations" \
    $ENVARG --entrypoint sh \
    migrate/migrate:v4.18.2 -c 'exec migrate -path /migrations -database "$DATABASE_URL" "'"$1"'" "'"$2"'"'
else
  # shellcheck disable=SC2086
  exec docker run --rm --network "$NET" \
    -v "$DIR:/migrations" \
    $ENVARG --entrypoint sh \
    migrate/migrate:v4.18.2 -c 'exec migrate -path /migrations -database "$DATABASE_URL" "'"$1"'"'
fi
