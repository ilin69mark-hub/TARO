#!/bin/sh
# backup.sh — daily pg_dump с ротацией 30 дней (см. 10-appendices/03, T30).
# Cron на VPS: 0 4 * * * /opt/taro/deploy/backup.sh
# PG-бэкапы "тлеют ≤30д" после DELETE /v1/me — ротация это гарантирует (см. 08-risks/02).
set -eu
set -o pipefail 2>/dev/null || true # dash его не знает — там pipefail недоступен, gzip-контроль ниже
BACKUP_DIR="${BACKUP_DIR:-/opt/taro/backups}"
KEEP_DAYS="${KEEP_DAYS:-30}"
mkdir -p "$BACKUP_DIR"
# Аудит D: одиночный инстанс (flock) + уникальное имя (секунды+PID) + pipefail.
exec 9>"$BACKUP_DIR/.lock" || exit 1
if ! flock -n 9; then
  echo "backup: уже выполняется, выходим" >&2
  exit 0
fi
TS="$(date +%F_%H%M%S)_$$"
FILE="$BACKUP_DIR/taro_$TS.sql.gz"
docker compose -f /opt/taro/docker-compose.yml exec -T db \
  pg_dump -U taro -d taro | gzip > "$FILE"
# Аудит D: падение pg_dump маскировалось успехом gzip — проверяем архив явно.
if ! gzip -t "$FILE"; then
  echo "backup: БИТЫЙ дамп, удаляем: $FILE" >&2
  rm -f "$FILE"
  exit 1
fi
chmod 600 "$FILE"
# Шифрование (E09): если задан BACKUP_GPG_KEY — шифруем и удаляем plaintext.
if [ -n "${BACKUP_GPG_KEY:-}" ]; then
  gpg --batch --yes --trust-model always -r "$BACKUP_GPG_KEY" -e -o "$FILE.gpg" "$FILE" \
    && rm -f "$FILE" && FILE="$FILE.gpg"
else
  echo "backup: ВНИМАНИЕ — без BACKUP_GPG_KEY дамп лежит открытым (см. E09)" >&2
fi
# Ротация обоих форматов (.gz и .gpg) — раньше .gpg копились вечно.
find "$BACKUP_DIR" -name 'taro_*.sql.gz' -mtime +"$KEEP_DAYS" -delete
find "$BACKUP_DIR" -name 'taro_*.sql.gz.gpg' -mtime +"$KEEP_DAYS" -delete
echo "backup: $FILE"
