#!/bin/sh
set -eu
umask 077

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
MIGRATIONS="$ROOT/api/migrations"
DEPLOY_ENV="${DEPLOY_ENV:-local}"

fail() {
  printf '%s\n' "$1" >&2
  exit 1
}

compose() {
  if [ "$DEPLOY_ENV" = prod ]; then
    docker compose --project-directory "$ROOT" -f "$ROOT/docker-compose.yml" -f "$ROOT/deploy/docker-compose.prod.yml" "$@"
  elif [ "$DEPLOY_ENV" = local ]; then
    docker compose --project-directory "$ROOT" -f "$ROOT/docker-compose.yml" "$@"
  else
    fail 'DEPLOY_ENV must be local or prod'
  fi
}

db_exec() {
  compose exec -T db "$@"
}

[ "${ALLOW_REPAIR:-0}" = 1 ] || fail 'ALLOW_REPAIR=1 is required'
[ -d "$MIGRATIONS" ] || fail 'migrations directory is missing'

latest=0
for migration in "$MIGRATIONS"/[0-9]*_*.up.sql; do
  [ -f "$migration" ] || continue
  name="${migration##*/}"
  number="${name%%_*}"
  case "$number" in
    ''|*[!0-9]*) continue ;;
  esac
  number=$((10#$number))
  [ "$number" -gt "$latest" ] && latest="$number"
done
[ "$latest" -gt 16 ] || fail 'no repair migrations found'

case "${DATABASE_URL:-}" in
  '')
    [ -n "${POSTGRES_PASSWORD:-}" ] || fail 'DATABASE_URL or POSTGRES_PASSWORD is required'
    DATABASE_URL="postgres://${POSTGRES_USER:-taro}:${POSTGRES_PASSWORD}@db:5432/${POSTGRES_DB:-taro}?sslmode=disable"
    export DATABASE_URL
    ;;
esac

state="$(db_exec psql -U "${POSTGRES_USER:-taro}" -d "${POSTGRES_DB:-taro}" -X -A -t -c "SELECT version::text || '|' || dirty::text FROM schema_migrations")"
[ "$state" = '1|t' ] || fail "unsupported migration state: $state"
for table in users plans spreads cards readings payments subscriptions single_entitlements entitlements referrals ai_logs app_config admin_audit push_subscriptions diary_entries push_preferences push_logs; do
  present="$(db_exec psql -U "${POSTGRES_USER:-taro}" -d "${POSTGRES_DB:-taro}" -X -A -t -c "SELECT to_regclass('public.$table') IS NOT NULL")"
  [ "$present" = t ] || fail "legacy table is missing: $table"
done
new_schema="$(db_exec psql -U "${POSTGRES_USER:-taro}" -d "${POSTGRES_DB:-taro}" -X -A -t -c "SELECT to_regclass('public.trial_grants') IS NULL AND to_regclass('public.share_tokens') IS NULL")"
[ "$new_schema" = t ] || fail 'database already contains post-016 schema; use a manual migration review'

backup_file="${BACKUP_FILE:-${TMPDIR:-/tmp}/taro-dirty-$(date +%Y%m%d%H%M%S).dump}"
compose exec -T db pg_dump -U "${POSTGRES_USER:-taro}" -d "${POSTGRES_DB:-taro}" --format=custom --no-owner --no-acl > "$backup_file"
chmod 600 "$backup_file"

container="$(compose ps -q db)"
db_exec rm -rf /tmp/taro-repair-migrations
docker cp "$MIGRATIONS/." "${container}:/tmp/taro-repair-migrations/" >/dev/null

db_exec sh -ec 'for f in /tmp/taro-repair-migrations/*.up.sql; do n="${f##*/}"; n="${n%%_*}"; if [ "$n" -ge 17 ]; then psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -X -v ON_ERROR_STOP=1 -f "$f" >/dev/null; fi; done'
compose --profile migration run --rm --no-deps -e DATABASE_URL="$DATABASE_URL" -e MIGRATE_COMMAND=force -e MIGRATE_STEPS="$latest" migrate >/dev/null
compose --profile migration run --rm --no-deps -e DATABASE_URL="$DATABASE_URL" -e EXPECTED_MIGRATION_VERSION="$latest" schema-check
db_exec rm -rf /tmp/taro-repair-migrations
printf 'dirty schema repaired; backup=%s\n' "$backup_file"
