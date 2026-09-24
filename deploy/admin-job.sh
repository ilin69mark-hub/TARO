#!/bin/sh
set -eu
umask 077

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
PATHNAME="${1:-}"
case "$PATHNAME" in
  /v1/admin/rotate-seasonal|/v1/admin/push-evening|/v1/admin/push-streak-risk|/v1/admin/remind-expiring) ;;
  *)
    printf '%s\n' 'unsupported admin job' >&2
    exit 1
    ;;
esac

LOCK_PATH="${ADMIN_LOCK_PATH:-/tmp/taro-admin.lock}"
LOCK_DIR="${ADMIN_LOCK_DIR:-/tmp/taro-admin.lock.d}"
LOCK_KIND=""
if command -v flock >/dev/null 2>&1; then
  exec 9>"$LOCK_PATH"
  flock -n 9 || exit 0
  LOCK_KIND=flock
else
  mkdir "$LOCK_DIR" 2>/dev/null || exit 0
  LOCK_KIND=dir
fi
cleanup() {
  if [ "$LOCK_KIND" = "dir" ]; then
    rmdir "$LOCK_DIR" 2>/dev/null || true
  fi
}
trap cleanup EXIT HUP INT TERM

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

if command -v curl >/dev/null 2>&1; then
  admin_endpoint="$(docker compose "$@" port api-admin 8082)"
  case "$admin_endpoint" in
    127.0.0.1:*) ;;
    *)
      printf '%s\n' 'admin port is not loopback' >&2
      exit 1
      ;;
  esac
  admin_port="${admin_endpoint##*:}"
  curl --fail --silent --show-error --max-time 5 "http://127.0.0.1:${admin_port}/healthz" >/dev/null
fi

docker compose "$@" exec -T api-admin sh -ec '
  path="$1"
  test -n "${ADMIN_API_TOKEN:-}"
  wget -qO- --post-data="{}" --header="X-Admin-Token: ${ADMIN_API_TOKEN}" --header="Content-Type: application/json" "http://127.0.0.1:8081${path}"
' sh "$PATHNAME"
