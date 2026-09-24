#!/bin/sh
set -eu

if [ -z "${DATABASE_URL:-}" ]; then
  [ -n "${POSTGRES_PASSWORD:-}" ] || {
    printf '%s\n' 'DATABASE_URL or POSTGRES_PASSWORD is required' >&2
    exit 1
  }
  DATABASE_URL="postgres://taro:${POSTGRES_PASSWORD}@db:5432/taro?sslmode=disable"
  export DATABASE_URL
fi
: "${EXPECTED_MIGRATION_VERSION:?EXPECTED_MIGRATION_VERSION is required}"
case "$EXPECTED_MIGRATION_VERSION" in
  ''|*[!0-9]*)
    printf '%s\n' 'EXPECTED_MIGRATION_VERSION must be an integer' >&2
    exit 1
    ;;
esac
EXPECTED_MIGRATION_VERSION=$((10#$EXPECTED_MIGRATION_VERSION))
export EXPECTED_MIGRATION_VERSION

present="$(psql "$DATABASE_URL" -X -A -t -v ON_ERROR_STOP=1 -c "SELECT to_regclass('public.schema_migrations') IS NOT NULL")"
if [ "$present" != "t" ]; then
  printf '%s\n' 'migration readiness: schema_migrations is missing' >&2
  exit 1
fi
state="$(psql "$DATABASE_URL" -X -A -t -v ON_ERROR_STOP=1 -c "SELECT version::text || '|' || dirty::text FROM schema_migrations")"
version="${state%%|*}"
dirty="${state#*|}"
if [ "$dirty" != "false" ]; then
  printf '%s\n' 'migration readiness: database is dirty' >&2
  exit 1
fi
if [ "$version" != "$EXPECTED_MIGRATION_VERSION" ]; then
  printf 'migration readiness: version %s, expected %s\n' "$version" "$EXPECTED_MIGRATION_VERSION" >&2
  exit 1
fi
missing="$(psql "$DATABASE_URL" -X -A -t -v ON_ERROR_STOP=1 -c "SELECT COALESCE(string_agg(required.name, ','), '') FROM (VALUES ('users'), ('plans'), ('spreads'), ('readings'), ('push_subscriptions'), ('push_preferences'), ('push_logs'), ('trial_grants'), ('share_tokens')) AS required(name) WHERE to_regclass('public.' || required.name) IS NULL")"
if [ -n "$missing" ]; then
  printf 'migration readiness: missing tables: %s\n' "$missing" >&2
  exit 1
fi
printf '%s\n' "migration readiness: schema version $version is clean"
