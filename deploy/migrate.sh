#!/bin/sh
set -eu
umask 077

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
MIGRATIONS="$ROOT/api/migrations"
ENV_FILE=""
TEMP_ENV=""

usage() {
  printf '%s\n' 'usage: migrate.sh up|down [steps]|verify|manifest' >&2
  exit 1
}

manifest_error() {
  printf 'migration manifest: %s\n' "$1" >&2
  exit 1
}

decimal_value() {
  value=$1
  case "$value" in
    ''|*[!0-9]*) return 1 ;;
  esac
  while [ "$value" != "0" ] && [ "${value#0}" != "$value" ]; do
    value=${value#0}
  done
  printf '%s' "$value"
}

check_manifest() {
  latest=0
  up_count=0
  seen_versions=" "
  for migration in "$MIGRATIONS"/*.sql; do
    [ -f "$migration" ] || continue
    name="${migration##*/}"
    case "$name" in
      [0-9]*_*.up.sql)
        version="${name%%_*}"
        case "$version" in
          ''|*[!0-9]*) manifest_error "invalid up migration filename: $name" ;;
        esac
        number=$(decimal_value "$version")
        [ "$number" -gt 0 ] || manifest_error "migration version must be positive: $name"
        case "$seen_versions" in
          *" $number "*) manifest_error "duplicate migration version $number: $name" ;;
        esac
        seen_versions="$seen_versions$number "
        up_count=$((up_count + 1))
        base="${name%.up.sql}"
        [ -f "$MIGRATIONS/$base.down.sql" ] || manifest_error "missing down migration for $name"
        if [ "$number" -gt "$latest" ]; then
          latest="$number"
        fi
        ;;
      [0-9]*_*.down.sql)
        base="${name%.down.sql}"
        [ -f "$MIGRATIONS/$base.up.sql" ] || manifest_error "orphan down migration: $name"
        ;;
      *)
        manifest_error "unsupported migration file: $name"
        ;;
    esac
  done
  [ "$up_count" -gt 0 ] || manifest_error 'no up migrations found'
  number=1
  while [ "$number" -le "$latest" ]; do
    case "$seen_versions" in
      *" $number "*) ;;
      *) manifest_error "missing migration version $number" ;;
    esac
    number=$((number + 1))
  done
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
  up|verify|down) ;;
  manifest) [ "$#" -eq 1 ] || usage ;;
  *) usage ;;
esac
if [ -n "$STEPS" ]; then
  case "$STEPS" in
    ''|*[!0-9]*) usage ;;
  esac
  [ "$COMMAND" = "down" ] || usage
  STEPS=$(decimal_value "$STEPS")
fi

if [ -n "${DATABASE_URL_FILE:-}" ] && [ -n "${DATABASE_URL:-}" ]; then
  printf '%s\n' 'DATABASE_URL and DATABASE_URL_FILE are mutually exclusive' >&2
  exit 1
fi

check_manifest
if [ "$COMMAND" = manifest ]; then
  printf 'migration manifest: %s\n' "$latest"
  exit 0
fi

DOWN_STEPS="${STEPS:-1}"
MIGRATION_STEPS="$STEPS"
if [ "$COMMAND" = "down" ]; then
  MIGRATION_STEPS="$DOWN_STEPS"
  [ "${ALLOW_DOWN:-0}" = "1" ] || {
    printf '%s\n' 'down requires ALLOW_DOWN=1' >&2
    exit 1
  }
  printf 'Roll back %s migration(s)? Type YES: ' "$DOWN_STEPS"
  IFS= read -r answer || answer=''
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
    awk '!/^[[:space:]]*(export[[:space:]]+)?DATABASE_URL[[:space:]]*=/' "$ROOT/.env" > "$TEMP_ENV"
  fi
  printf '\nDATABASE_URL=%s\n' "$database_url" >> "$TEMP_ENV"
  ENV_FILE="$TEMP_ENV"
elif [ -f "$ROOT/.env" ]; then
  ENV_FILE="$ROOT/.env"
fi

wait_for_dependencies() {
  if [ -n "$ENV_FILE" ]; then
    docker compose "$@" --env-file "$ENV_FILE" up -d --no-recreate --wait --wait-timeout 120 db cache
  else
    docker compose "$@" up -d --no-recreate --wait --wait-timeout 120 db cache
  fi
}

compose_run() {
  if [ -n "$ENV_FILE" ]; then
    docker compose "$@" --env-file "$ENV_FILE" run --rm --no-deps \
      -e MIGRATION_PREFLIGHT=0 \
      -e "EXPECTED_MIGRATION_VERSION=$latest" schema-check
  else
    docker compose "$@" run --rm --no-deps \
      -e MIGRATION_PREFLIGHT=0 \
      -e "EXPECTED_MIGRATION_VERSION=$latest" schema-check
  fi
}

preflight_down() {
  if [ -n "$ENV_FILE" ]; then
    docker compose "$@" --env-file "$ENV_FILE" run --rm --no-deps \
      -e MIGRATION_PREFLIGHT=1 \
      -e "MIGRATION_DOWN_STEPS=$DOWN_STEPS" \
      -e "MIGRATION_LATEST_VERSION=$latest" \
      schema-check
  else
    docker compose "$@" run --rm --no-deps \
      -e MIGRATION_PREFLIGHT=1 \
      -e "MIGRATION_DOWN_STEPS=$DOWN_STEPS" \
      -e "MIGRATION_LATEST_VERSION=$latest" \
      schema-check
  fi
}

run_migration() {
  if [ "$COMMAND" = "down" ]; then
    if [ -n "$ENV_FILE" ]; then
      docker compose "$@" --env-file "$ENV_FILE" run --rm --no-deps \
        -e "MIGRATE_COMMAND=$COMMAND" \
        -e "MIGRATE_STEPS=$MIGRATION_STEPS" \
        -e "PGOPTIONS=-c taro.migration_029_preflight=confirmed" migrate
    else
      docker compose "$@" run --rm --no-deps \
        -e "MIGRATE_COMMAND=$COMMAND" \
        -e "MIGRATE_STEPS=$MIGRATION_STEPS" \
        -e "PGOPTIONS=-c taro.migration_029_preflight=confirmed" migrate
    fi
  elif [ -n "$ENV_FILE" ]; then
    docker compose "$@" --env-file "$ENV_FILE" run --rm --no-deps \
      -e "MIGRATE_COMMAND=$COMMAND" \
      -e "MIGRATE_STEPS=$MIGRATION_STEPS" migrate
  else
    docker compose "$@" run --rm --no-deps \
      -e "MIGRATE_COMMAND=$COMMAND" \
      -e "MIGRATE_STEPS=$MIGRATION_STEPS" migrate
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
    preflight_down "$@"
    run_migration "$@"
    ;;
esac
