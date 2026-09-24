#!/bin/sh
set -eu
umask 077

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
BACKUP_DIR="${BACKUP_DIR:-/opt/taro/backups}"
KEEP_DAYS="${KEEP_DAYS:-30}"
POSTGRES_USER="${POSTGRES_USER:-taro}"
POSTGRES_DB="${POSTGRES_DB:-taro}"

case "$KEEP_DAYS" in
  ''|*[!0-9]*)
    printf '%s\n' 'KEEP_DAYS must be a non-negative integer' >&2
    exit 1
    ;;
esac
[ "$KEEP_DAYS" -gt 0 ] || {
  printf '%s\n' 'KEEP_DAYS must be greater than zero' >&2
  exit 1
}
mkdir -p "$BACKUP_DIR"
chmod 700 "$BACKUP_DIR"
LOCK_PATH="$BACKUP_DIR/.lock"
LOCK_DIR="$BACKUP_DIR/.lock.d"
LOCK_KIND=""
if command -v flock >/dev/null 2>&1; then
  exec 9>"$LOCK_PATH"
  if ! flock -n 9; then
    printf '%s\n' 'backup: already running' >&2
    exit 0
  fi
  LOCK_KIND=flock
else
  if ! mkdir "$LOCK_DIR" 2>/dev/null; then
    printf '%s\n' 'backup: already running' >&2
    exit 0
  fi
  LOCK_KIND=dir
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

if [ -z "${BACKUP_GPG_KEY:-}" ] && [ -f "$ROOT/.env" ]; then
  BACKUP_GPG_KEY="$(awk -F= '$1 == "BACKUP_GPG_KEY" {sub(/^[^=]*=/, ""); print; exit}' "$ROOT/.env")"
  case "$BACKUP_GPG_KEY" in
    \"*\") BACKUP_GPG_KEY="${BACKUP_GPG_KEY#\"}"; BACKUP_GPG_KEY="${BACKUP_GPG_KEY%\"}" ;;
    \'*\') BACKUP_GPG_KEY="${BACKUP_GPG_KEY#\'}"; BACKUP_GPG_KEY="${BACKUP_GPG_KEY%\'}" ;;
  esac
  export BACKUP_GPG_KEY
fi

TS="$(date +%F_%H%M%S)_$$"
FILE="$BACKUP_DIR/taro_$TS.sql.gz"
PLAIN=""
KEEP_FILE=0
cleanup() {
  if [ -n "$PLAIN" ]; then
    rm -f -- "$PLAIN"
  fi
  if [ "$KEEP_FILE" -ne 1 ] && [ -n "$FILE" ]; then
    rm -f -- "$FILE" "$FILE.gpg"
  fi
  if [ "$LOCK_KIND" = "dir" ]; then
    rmdir "$LOCK_DIR" 2>/dev/null || true
  fi
}
trap cleanup EXIT HUP INT TERM
PLAIN="$(mktemp "$BACKUP_DIR/.taro_dump.XXXXXX")"

docker compose "$@" exec -T db pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" > "$PLAIN"
gzip -c "$PLAIN" > "$FILE"
rm -f -- "$PLAIN"
if ! gzip -t "$FILE"; then
  rm -f -- "$FILE"
  printf '%s\n' 'backup: gzip verification failed' >&2
  exit 1
fi
chmod 600 "$FILE"

if [ -n "${BACKUP_GPG_KEY:-}" ]; then
  if ! gpg --batch --yes --trust-model always -r "$BACKUP_GPG_KEY" -e -o "$FILE.gpg" "$FILE"; then
    rm -f -- "$FILE" "$FILE.gpg"
    printf '%s\n' 'backup: encryption failed' >&2
    exit 1
  fi
  rm -f -- "$FILE"
  FILE="$FILE.gpg"
elif [ "${DEPLOY_ENV:-local}" = "prod" ]; then
  rm -f -- "$FILE"
  printf '%s\n' 'backup: BACKUP_GPG_KEY is required in production' >&2
  exit 1
else
  printf '%s\n' 'backup: warning, BACKUP_GPG_KEY is not set' >&2
fi
KEEP_FILE=1

find "$BACKUP_DIR" -type f -name 'taro_*.sql.gz' -mtime "+$KEEP_DAYS" -delete
find "$BACKUP_DIR" -type f -name 'taro_*.sql.gz.gpg' -mtime "+$KEEP_DAYS" -delete
printf 'backup: %s\n' "$FILE"
