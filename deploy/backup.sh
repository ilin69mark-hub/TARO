#!/bin/sh
# backup.sh — daily pg_dump с ротацией 30 дней (см. 10-appendices/03, T30).
# Cron на VPS: 0 4 * * * /opt/taro/deploy/backup.sh
# PG-бэкапы "тлеют ≤30д" после DELETE /v1/me — ротация это гарантирует (см. 08-risks/02).
set -eu
BACKUP_DIR="${BACKUP_DIR:-/opt/taro/backups}"
KEEP_DAYS="${KEEP_DAYS:-30}"
mkdir -p "$BACKUP_DIR"
TS="$(date +%F_%H%M)"
FILE="$BACKUP_DIR/taro_$TS.sql.gz"
docker compose -f /opt/taro/docker-compose.yml exec -T db \
  pg_dump -U taro -d taro | gzip > "$FILE"
chmod 600 "$FILE"
# Шифрование (E09): если задан BACKUP_GPG_KEY — шифруем и удаляем plaintext.
if [ -n "${BACKUP_GPG_KEY:-}" ]; then
  gpg --batch --yes --trust-model always -r "$BACKUP_GPG_KEY" -e -o "$FILE.gpg" "$FILE" \
    && rm -f "$FILE" && FILE="$FILE.gpg"
else
  echo "backup: ВНИМАНИЕ — без BACKUP_GPG_KEY дамп лежит открытым (см. E09)" >&2
fi
find "$BACKUP_DIR" -name 'taro_*.sql.gz' -mtime +"$KEEP_DAYS" -delete
echo "backup: $FILE"
