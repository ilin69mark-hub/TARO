#!/bin/sh
# Обновляет deploy/cloudflare-ips.conf по официальному списку Cloudflare.
#
# Список сетей Cloudflare меняется. Пока в файле устаревшая сеть, а Cloudflare
# уже стоит, nginx перестаёт доверять реальному IP этой части трафика: $remote_addr
# остаётся адресом Cloudflare, и IP-лимиты начинают считать всех одним человеком.
# То есть устаревший файл ломает прод ТИХО — без ошибок в логах.
#
# Запуск: sh deploy/update-cf-ips.sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
OUT="$ROOT/deploy/cloudflare-ips.conf"
CACHE=$(mktemp)
trap 'rm -f "$CACHE"' EXIT

fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsS --max-time 20 "$1"
  else
    wget -qO- -T 20 "$1"
  fi
}

fetch https://www.cloudflare.com/ips-v4 > "$CACHE"
printf '\n' >> "$CACHE"
fetch https://www.cloudflare.com/ips-v6 >> "$CACHE"

# Оставляем только строки-подсети: официальный список отдаётся плейнтекстом, но
# при смене разметки мусор в конфиг nginx попасть не должен.
NETS=$(grep -E '^[0-9a-fA-F:.]+/[0-9]+$' "$CACHE" | sort -u)
if [ -z "$NETS" ]; then
  echo "не удалось получить список сетей — файл не тронут" >&2
  exit 1
fi

# Шапка = всё до первой пустой строки. Две тонкости, обе стоили прогона:
#   • если не отбросить строки, которые скрипт добавляет сам ("Снято" и
#     "Всего сетей"), то каждый запуск дописывает ещё одну копию — шапка
#     распухала до четырёх "Всего сетей";
#   • дата и счётчик добавляются заново уже ПОСЛЕ фильтрации.
HEAD=$(awk 'BEGIN{h=1} /^$/{h=0} h{print}' "$ROOT/deploy/cloudflare-ips.conf" 2>/dev/null \
  | grep -vE '^# (Снято|Всего сетей):')
TODAY=$(date -u +%Y-%m-%d)
{
  printf '%s\n' "$HEAD" | sed "s/^# Снято: .*/# Снято: $TODAY/"
  # Подсчёт по наличию ':' — IPv6. Первой версией эти два числа стояли
  # наоборот, и файл рапортовал «7 IPv4 + 15 IPv6».
  printf '# Всего сетей: %s IPv4 + %s IPv6\n' \
    "$(printf '%s\n' "$NETS" | grep -vc ':')" \
    "$(printf '%s\n' "$NETS" | grep -c ':')"
  echo
  # Пишем ГОТОВЫЕ директивы, а не голые сети. Так список нельзя рассинхронизировать
  # с include в nginx.conf: в main-конфиге нет ни плейсхолдера, ни 0.0.0.0/0,
  # который доверил бы заголовок кому угодно.
  printf '%s\n' "$NETS" | sed 's/^/set_real_ip_from /; s/$/;/'
} > "$OUT.tmp"

# Порядок в файле значения не имеет, но проверка на изменение полезна в логе.
if cmp -s "$OUT" "$OUT.tmp"; then
  rm -f "$OUT.tmp"
  echo "список не изменился"
  exit 0
fi
mv "$OUT.tmp" "$OUT"
echo "обновлено: $OUT"
