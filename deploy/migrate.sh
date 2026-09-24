#!/bin/sh
set -eu
umask 077

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
MIGRATIONS="$ROOT/api/migrations"
ENV_FILE=""
TEMP_ENV=""

usage() {
  printf '%s\n' 'usage: migrate.sh up|down [steps]|verify' >&2
  exit 1
}

cleanup() {
  if [ -n "$TEMP_ENV" ]; then
    rm -f -- "$TEMP_ENV"
  fi
}
trap cleanup EXIT HUP INT TERM

[ "$#" -ge 1 ] || usage
COMMAND="$1"
STEPS="${2:-}"
case "$COMMAND" in
  up|verify) ;;
  down) ;;
  *) usage ;;
esac
if [ -n "$STEPS" ]; then
  case "$STEPS" in
    ''|*[!0-9]*) usage ;;
  esac
  [ "$COMMAND" = "down" ] || usage
fi
if [ "$COMMAND" = "down" ]; then
  [ "${ALLOW_DOWN:-0}" = "1" ] || {
    printf '%s\n' 'down requires ALLOW_DOWN=1' >&2
    exit 1
  }
  printf 'Roll back %s migration(s)? Type YES: ' "$STEPS"
  IFS= read -r answer
  [ "$answer" = "YES" ] || {
    printf '%s\n' 'cancelled' >&2
    exit 1
  }
fi

case "${DEPLOY_ENV:-local}" in
  local)
    set -- -f "$ROOT/docker-compose.yml"
    ;;
  prod)
    [ -f "$ROOT/deploy/docker-compose.prod.yml" ] || {
      printf '%s\n' 'production Compose override is missing' >&2
      exit 1
    }
    set -- -f "$ROOT/docker-compose.yml" -f "$ROOT/deploy/docker-compose.prod.yml"
    ;;
  *)
    printf '%s\n' 'DEPLOY_ENV must be local or prod' >&2
    exit 1
    ;;
esac

if [ -n "${DATABASE_URL_FILE:-}" ]; then
  [ -r "$DATABASE_URL_FILE" ] || {
    printf '%s\n' 'DATABASE_URL_FILE is not readable' >&2
    exit 1
  }
  IFS= read -r database_url < "$DATABASE_URL_FILE" || true
  [ -n "$database_url" ] || {
    printf '%s\n' 'DATABASE_URL_FILE is empty' >&2
    exit 1
  }
  TEMP_ENV="$(mktemp "${TMPDIR:-/tmp}/taro-migrate.XXXXXX")"
  chmod 600 "$TEMP_ENV"
  if [ -f "$ROOT/.env" ]; then
    awk '!/^[[:space:]]*DATABASE_URL[[:space:]]*=/' "$ROOT/.env" > "$TEMP_ENV"
  fi
  printf '\nDATABASE_URL=%s\n' "$database_url" >> "$TEMP_ENV"
  ENV_FILE="$TEMP_ENV"
elif [ -f "$ROOT/.env" ]; then
  ENV_FILE="$ROOT/.env"
fi

latest=0
for migration in "$MIGRATIONS"/[0-9]*_*.up.sql; do
  [ -f "$migration" ] || continue
  name="${migration##*/}"
  number="${name%%_*}"
  case "$number" in
    ''|*[!0-9]*) continue ;;
  esac
  number=$((10#$number))
  if [ "$number" -gt "$latest" ]; then
    latest="$number"
  fi
done
[ "$latest" -gt 0 ] || {
  printf '%s\n' 'no migration files found' >&2
  exit 1
}

wait_for_dependencies() {
  if [ -n "$ENV_FILE" ]; then
    docker compose "$@" --env-file "$ENV_FILE" up -d --no-recreate --wait --wait-timeout 120 db cache
  else
    docker compose "$@" up -d --no-recreate --wait --wait-timeout 120 db cache
  fi
}

compose_run() {
  if [ -n "$ENV_FILE" ]; then
    docker compose "$@" --env-file "$ENV_FILE" run --rm --no-deps -e "EXPECTED_MIGRATION_VERSION=$latest" schema-check
  else
    docker compose "$@" run --rm --no-deps -e "EXPECTED_MIGRATION_VERSION=$latest" schema-check
  fi
}

run_migration() {
  if [ -n "$ENV_FILE" ]; then
    docker compose "$@" --env-file "$ENV_FILE" run --rm --no-deps -e "MIGRATE_COMMAND=$COMMAND" -e "MIGRATE_STEPS=$STEPS" migrate
  else
    docker compose "$@" run --rm --no-deps -e "MIGRATE_COMMAND=$COMMAND" -e "MIGRATE_STEPS=$STEPS" migrate
  fi
}

wait_for_dependencies "$@"

case "$COMMAND" in
  up)
    run_migration "$@"
    compose_run "$@"
    ;;
  verify)
    compose_run "$@"
    ;;
  down)
    run_migration "$@"
    ;;
esac
