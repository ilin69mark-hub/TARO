#!/bin/sh
# check-env.sh — префлайн prod-окружения (A06/F-32).
# Список обязательных переменных НЕ хранится здесь: он выводится из самих
# compose-файлов (`${VAR:?…}`), поэтому новый требуемый ключ нельзя «забыть».
#
#   deploy/check-env.sh [--keys-only] [env-файл]
#
#   --keys-only   требовать только упоминания ключа (для .env.example и CI),
#                 без проверки непустого значения.
#   env-файл      по умолчанию .env; "-" — только переменные окружения.
#
# Переменная считается заданной, если она есть в окружении ИЛИ в файле с
# непустым значением. Выход 0 — всё на месте, 1 — список недостающих в stderr.
set -eu
umask 077

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

KEYS_ONLY=0
ENV_FILE="$ROOT/.env"
case "${1:-}" in
  --keys-only)
    KEYS_ONLY=1
    shift
    ENV_FILE="${1:-$ROOT/.env}"
    ;;
  -)
    ENV_FILE=""
    ;;
  '')
    ;;
  *)
    ENV_FILE="$1"
    ;;
esac
case "$ENV_FILE" in
  /*) ;;
  *) ENV_FILE="$ROOT/$ENV_FILE" ;;
esac

COMPOSE_FILES="$ROOT/docker-compose.yml $ROOT/deploy/docker-compose.prod.yml $ROOT/deploy/docker-compose.tls.yml"

required="$(grep -hoE '\$\{[A-Z_]+:\?[^}]*\}' $COMPOSE_FILES | sed -E 's/^\$\{([A-Z_]+):.*/\1/' | sort -u)"

if [ -z "$required" ]; then
  printf '%s\n' 'check-env.sh: no required variables found in compose files' >&2
  exit 1
fi

file_value() {
  # $1 — имя ключа; печатает значение из файла, если ключ там есть
  [ -n "$ENV_FILE" ] || return 1
  awk -v key="$1" '
    /^[[:space:]]*#/ { next }
    index($0, key "=") == 1 { print substr($0, length(key) + 2); found = 1; exit }
    END { exit(found ? 0 : 1) }
  ' "$ENV_FILE"
}

# Шаблонные dev-значения не считаются заданными: иначе копия .env.example
# без правок прошла бы префлайн и уехала в прод.
is_placeholder() {
  case "$1" in
    CHANGE_ME* | dev-only-* | taro_dev_only) return 0 ;;
    *) return 1 ;;
  esac
}

missing=""
placeholder=""
count=0
for key in $required; do
  count=$((count + 1))
  if [ "$KEYS_ONLY" -eq 1 ]; then
    if [ -n "$ENV_FILE" ] && grep -qE "^${key}=" "$ENV_FILE" 2>/dev/null; then
      continue
    fi
    missing="$missing $key"
    continue
  fi
  eval "value=\${$key:-}"
  if [ -z "${value:-}" ] || is_placeholder "$value"; then
    if [ -n "$ENV_FILE" ] && file_value "$key" >/dev/null 2>&1; then
      value="$(file_value "$key")"
      if [ -n "$value" ] && ! is_placeholder "$value"; then
        continue
      fi
      if is_placeholder "$value"; then
        placeholder="$placeholder $key"
        continue
      fi
    fi
    missing="$missing $key"
    continue
  fi
done

status=0
if [ -n "$missing" ]; then
  status=1
  printf 'STOP: %s required variable(s) missing or empty\n' "$(printf '%s' "$missing" | wc -w | tr -d ' ')" >&2
  printf '  required by compose (prod+tls):%s\n' "$missing" >&2
  printf '  see .env.example for the full contract and deploy/README.md for prod setup\n' >&2
fi
if [ -n "$placeholder" ]; then
  status=1
  printf 'STOP: %s variable(s) still hold template/dev values\n' "$(printf '%s' "$placeholder" | wc -w | tr -d ' ')" >&2
  printf '  placeholders:%s\n' "$placeholder" >&2
fi
if [ -n "$ENV_FILE" ] && [ "$status" -ne 0 ]; then
  printf '  env file checked: %s\n' "$ENV_FILE" >&2
fi
[ "$status" -eq 0 ] || exit "$status"
printf 'check-env.sh: all %s required variables are present\n' "$count"
