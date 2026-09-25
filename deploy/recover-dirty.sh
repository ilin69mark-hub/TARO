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

manifest_error() {
  fail "migration manifest: $1"
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

check_legacy_schema() {
  state="$(db_exec psql -U "${POSTGRES_USER:-taro}" -d "${POSTGRES_DB:-taro}" -X -A -t -v ON_ERROR_STOP=1 -c "SELECT version::text || '|' || dirty::text FROM schema_migrations")"
  [ "$state" = '1|true' ] || fail "unsupported migration state: $state"
  for table in schema_migrations users plans spreads cards readings payments subscriptions single_entitlements entitlements referrals ai_logs app_config admin_audit push_subscriptions diary_entries push_preferences push_logs; do
    present="$(db_exec psql -U "${POSTGRES_USER:-taro}" -d "${POSTGRES_DB:-taro}" -X -A -t -v ON_ERROR_STOP=1 -c "SELECT COALESCE((SELECT c.relkind IN ('r', 'p') FROM pg_class c WHERE c.oid = to_regclass('public.$table')), false)")"
    [ "$present" = t ] || fail "legacy table is missing: $table"
  done
  new_schema="$(db_exec psql -U "${POSTGRES_USER:-taro}" -d "${POSTGRES_DB:-taro}" -X -A -t -v ON_ERROR_STOP=1 -c "SELECT to_regclass('public.trial_grants') IS NULL AND to_regclass('public.share_tokens') IS NULL")"
  [ "$new_schema" = t ] || fail 'database already contains post-016 schema; use a manual migration review'

  missing_seed="$(db_exec psql -U "${POSTGRES_USER:-taro}" -d "${POSTGRES_DB:-taro}" -X -A -t -v ON_ERROR_STOP=1 -c "
SELECT COALESCE(string_agg(required.name, ', ' ORDER BY required.name), '')
FROM (
  SELECT 'cards: expected 78 contiguous rows' AS name
  WHERE NOT COALESCE((
    SELECT count(*) = 78
       AND count(DISTINCT id) = 78
       AND min(id) = 0
       AND max(id) = 77
    FROM cards
  ), false)
  UNION ALL
  SELECT 'plan:' || required.code
  FROM (VALUES ('free'), ('month_299'), ('year_2490'), ('single_99'), ('trial_3d'), ('referral_bonus')) AS required(code)
  WHERE NOT EXISTS (SELECT 1 FROM plans WHERE plans.code = required.code)
  UNION ALL
  SELECT 'spread:' || required.code
  FROM (VALUES ('daily'), ('three'), ('love'), ('decision'), ('celtic'), ('fullmoon'), ('newyear')) AS required(code)
  WHERE NOT EXISTS (SELECT 1 FROM spreads WHERE spreads.code = required.code)
  UNION ALL
  SELECT 'app_config:' || required.key
  FROM (VALUES ('free.daily_limit'), ('love.free_weekly'), ('history.free_limit'), ('trial'), ('referral'), ('copy.paywall_title'), ('copy.paywall_desc'), ('copy.paywall_cta'), ('ai')) AS required(key)
  WHERE NOT EXISTS (SELECT 1 FROM app_config WHERE app_config.key = required.key)
  UNION ALL
  SELECT 'seed_admin:00000000-0000-0000-0000-000000000001'
  WHERE NOT EXISTS (
    SELECT 1 FROM users
    WHERE id = '00000000-0000-0000-0000-000000000001'::uuid
      AND role = 'admin'
  )
) AS required")"
  [ -z "$missing_seed" ] || fail "legacy seed preflight failed: $missing_seed"
}

[ -d "$MIGRATIONS" ] || fail 'migrations directory is missing'
[ -z "${DATABASE_URL:-}" ] || fail 'DATABASE_URL is not supported by legacy recovery; recovery validates and repairs only the Compose db service'
[ -z "${DATABASE_URL_FILE:-}" ] || fail 'DATABASE_URL_FILE is not supported by legacy recovery; recovery validates and repairs only the Compose db service'
[ "${ALLOW_REPAIR:-0}" = 1 ] || fail 'ALLOW_REPAIR=1 is required'
[ -n "${POSTGRES_PASSWORD:-}" ] || fail 'POSTGRES_PASSWORD is required for the Compose db service'

check_manifest
[ "$latest" -gt 16 ] || fail 'no repair migrations found'

DATABASE_URL="postgres://${POSTGRES_USER:-taro}:${POSTGRES_PASSWORD}@db:5432/${POSTGRES_DB:-taro}?sslmode=disable"
export DATABASE_URL

check_legacy_schema

backup_file="${BACKUP_FILE:-${TMPDIR:-/tmp}/taro-dirty-$(date +%Y%m%d%H%M%S).dump}"
compose exec -T db pg_dump -U "${POSTGRES_USER:-taro}" -d "${POSTGRES_DB:-taro}" --format=custom --no-owner --no-acl > "$backup_file"
chmod 600 "$backup_file"

container="$(compose ps -q db)"
[ -n "$container" ] || fail 'database container is not running'
db_exec rm -rf /tmp/taro-repair-migrations
docker cp "$MIGRATIONS/." "${container}:/tmp/taro-repair-migrations/" >/dev/null

db_exec sh -ec 'for f in /tmp/taro-repair-migrations/*.up.sql; do [ -f "$f" ] || continue; n="${f##*/}"; n="${n%%_*}"; while [ "$n" != "0" ] && [ "${n#0}" != "$n" ]; do n=${n#0}; done; if [ "$n" -ge 17 ]; then psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -X -v ON_ERROR_STOP=1 -f "$f" >/dev/null; fi; done'
compose --profile migration run --rm --no-deps -e DATABASE_URL="$DATABASE_URL" -e MIGRATE_COMMAND=force -e MIGRATE_STEPS="$latest" migrate >/dev/null
compose --profile migration run --rm --no-deps -e DATABASE_URL="$DATABASE_URL" -e EXPECTED_MIGRATION_VERSION="$latest" -e MIGRATION_PREFLIGHT=0 -e SCHEMA_READINESS_MODE=schema schema-check
db_exec rm -rf /tmp/taro-repair-migrations
printf 'dirty schema repaired; backup=%s\n' "$backup_file"
