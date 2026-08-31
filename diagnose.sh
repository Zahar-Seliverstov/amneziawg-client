#!/bin/bash
#
# Автономная диагностика подключения AmneziaWG.
#
#   ./diagnose.sh
#
# Скрипт работает БЕЗ интернета и сам возвращает всё назад.
# Что делает: гасит AmneziaVPN, поднимает наш туннель, снимает полную
# картину (лог хендшейка, маршруты, DNS, счётчики), отключает наш туннель
# и запускает AmneziaVPN обратно.
#
# AmneziaVPN будет недоступен около минуты. Возврат гарантирован через trap:
# сработает даже при ошибке, Ctrl+C или падении скрипта.
#
# Результат: diag-report.txt (ключи из него вырезаны).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKEND_BIN="$SCRIPT_DIR/backend/build/awg-client"
REPORT="$SCRIPT_DIR/diag-report.txt"
BACKEND_LOG="$(mktemp /tmp/awg-diag-backend.XXXXXX.log)"
DIAG_PORT=8099
CONFIG_FILE="$HOME/.config/awg-client/config.json"

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; BLUE='\033[0;34m'; NC='\033[0m'
say()  { echo -e "${BLUE}[..]${NC} $1"; }
ok()   { echo -e "${GREEN}[OK]${NC} $1"; }
warn() { echo -e "${YELLOW}[!!]${NC} $1"; }
err()  { echo -e "${RED}[XX]${NC} $1"; }

if [ "${EUID:-$(id -u)}" -eq 0 ]; then
    err "Запускай обычным пользователем, без sudo: ./diagnose.sh"
    exit 1
fi

# в отчёт и на экран одновременно
rep() { echo "$*" >> "$REPORT"; }
sec() { echo "" >> "$REPORT"; echo "═══ $* ═══" >> "$REPORT"; }
run() { echo "" >> "$REPORT"; echo "\$ $*" >> "$REPORT"; "$@" >> "$REPORT" 2>&1; }

AMNEZIA_WAS_RUNNING=0
DIAG_BACKEND_PID=""

restore() {
    trap - INT TERM EXIT
    echo ""
    say "Возвращаю всё назад..."

    # отключаем наш туннель
    curl -s -m 5 -X POST "http://127.0.0.1:$DIAG_PORT/api/vpn/disconnect" >/dev/null 2>&1
    sleep 2

    if [ -n "$DIAG_BACKEND_PID" ]; then
        sudo -n pkill -TERM -f "^$BACKEND_BIN -host 127.0.0.1 -port $DIAG_PORT" 2>/dev/null
        sleep 1
        sudo -n pkill -KILL -f "^$BACKEND_BIN -host 127.0.0.1 -port $DIAG_PORT" 2>/dev/null
    fi

    # подчищаем свои следы на случай, если туннель не убрался сам
    sudo -n ip link delete awg0 2>/dev/null

    if [ "$AMNEZIA_WAS_RUNNING" -eq 1 ]; then
        say "Запускаю AmneziaVPN обратно..."
        sudo -n systemctl start AmneziaVPN 2>/dev/null
        for i in $(seq 1 20); do
            if systemctl is-active --quiet AmneziaVPN; then break; fi
            sleep 1
        done
        if systemctl is-active --quiet AmneziaVPN; then
            ok "AmneziaVPN снова работает"
            echo ""
            warn "Подключись в окне AmneziaVPN, если оно не подключилось само."
        else
            err "AmneziaVPN не поднялся! Запусти вручную: sudo systemctl start AmneziaVPN"
        fi
    fi

    echo ""
    ok "Отчёт готов: $REPORT"
    echo ""
    echo "Открой его и пришли содержимое. Приватные ключи из него вырезаны."
    exit 0
}
trap restore INT TERM EXIT

echo ""
echo "╔════════════════════════════════════════════════╗"
echo "║   Диагностика подключения AmneziaWG            ║"
echo "╚════════════════════════════════════════════════╝"
echo ""
warn "AmneziaVPN будет отключён примерно на минуту."
warn "Скрипт вернёт его сам, даже если что-то пойдёт не так."
echo ""

[ -x "$BACKEND_BIN" ] || { err "Нет собранного backend. Сначала: ./start.sh"; exit 1; }
[ -f "$CONFIG_FILE" ] || { err "Нет конфигов. Добавь конфиг в интерфейсе."; exit 1; }

say "Нужен пароль sudo"
sudo -v || { err "Без sudo не получится"; exit 1; }
( while true; do sleep 50; kill -0 "$$" 2>/dev/null || exit; sudo -n true 2>/dev/null || exit; done ) &

: > "$REPORT"
rep "Диагностика AmneziaWG Web Client"
rep "Дата: $(date '+%Y-%m-%d %H:%M:%S')"
rep "Ядро: $(uname -r)"

sec "СОСТОЯНИЕ ДО"
run ip -brief address show
run ip route show
run ip -6 route show
run resolvectl status --no-pager

# ── гасим AmneziaVPN ────────────────────────────────────────────────────────
if systemctl is-active --quiet AmneziaVPN; then
    AMNEZIA_WAS_RUNNING=1
    say "Останавливаю AmneziaVPN..."
    sudo -n systemctl stop AmneziaVPN
    sleep 3
    ok "AmneziaVPN остановлен"
fi
sudo -n ip link delete amn0 2>/dev/null
sudo -n ip link delete awg0 2>/dev/null

sec "СОСТОЯНИЕ БЕЗ VPN"
run ip -brief address show
run ip route show

# ── свой экземпляр backend, лог в файл ──────────────────────────────────────
say "Запускаю backend для опыта на порту $DIAG_PORT..."
sudo -n env HOME="$HOME" "$BACKEND_BIN" -host 127.0.0.1 -port "$DIAG_PORT" -config "$CONFIG_FILE" \
    > "$BACKEND_LOG" 2>&1 &
DIAG_BACKEND_PID=$!

for i in $(seq 1 20); do
    curl -sf -o /dev/null -m 2 "http://127.0.0.1:$DIAG_PORT/api/vpn/status" && break
    sleep 1
done
curl -sf -o /dev/null -m 2 "http://127.0.0.1:$DIAG_PORT/api/vpn/status" || { err "Backend не поднялся"; exit 1; }
ok "Backend работает"

# Берём первый конфиг; можно указать имя: ./diagnose.sh "имя конфига"
WANT_NAME="${1:-}"
CONFIGS_JSON="$(curl -s -m 5 "http://127.0.0.1:$DIAG_PORT/api/configs")"
read -r CONFIG_ID CONFIG_NAME <<< "$(printf '%s' "$CONFIGS_JSON" | python3 -c '
import sys, json
want = sys.argv[1] if len(sys.argv) > 1 else ""
try:
    d = json.load(sys.stdin)
except Exception:
    d = []
if not d:
    print(" ")
else:
    c = next((x for x in d if x.get("name") == want), d[0])
    print(c["id"], c["name"])
' "$WANT_NAME" 2>/dev/null)"

if [ -z "$CONFIG_ID" ]; then
    err "В приложении нет ни одного конфига"
    exit 1
fi
ok "Конфиг: $CONFIG_NAME"

sec "ПАРАМЕТРЫ КОНФИГА (без ключей)"
curl -s -m 5 "http://127.0.0.1:$DIAG_PORT/api/configs" | python3 -c '
import sys, json
d = json.load(sys.stdin)
c = d[0]
i = c["interface"]
print("Address:", i.get("address"))
print("DNS:", i.get("dns"))
print("MTU:", i.get("mtu"))
print("Jc/Jmin/Jmax:", i.get("jc"), i.get("jmin"), i.get("jmax"))
print("S1..S4:", i.get("s1"), i.get("s2"), i.get("s3"), i.get("s4"))
print("H1..H4:", i.get("h1"), i.get("h2"), i.get("h3"), i.get("h4"))
print("HeaderProtectionKey задан:", bool(i.get("header_protection_key")))
print("ContentPaddingAddition:", i.get("content_padding_addition"))
print("RekeyAfterTime:", i.get("rekey_after_time"))
for p in c["peers"]:
    print("Peer endpoint:", p.get("endpoint"))
    print("Peer allowed_ips:", p.get("allowed_ips"))
    print("Peer keepalive:", p.get("persistent_keepalive"))
    print("PresharedKey задан:", bool(p.get("preshared_key")))
' >> "$REPORT" 2>&1

# ── подключаемся ────────────────────────────────────────────────────────────
say "Подключаю туннель..."
sec "ОТВЕТ НА CONNECT"
curl -s -m 20 -X POST "http://127.0.0.1:$DIAG_PORT/api/vpn/connect" \
    -H 'Content-Type: application/json' \
    -d "{\"config_id\":\"$CONFIG_ID\"}" >> "$REPORT" 2>&1
rep ""

say "Жду 20 секунд, чтобы прошёл хендшейк..."
sleep 20

sec "СТАТУС В ПРИЛОЖЕНИИ"
run curl -s -m 5 "http://127.0.0.1:$DIAG_PORT/api/vpn/status"
rep ""

sec "ИНТЕРФЕЙС awg0"
run ip -brief address show awg0
run ip -s link show awg0

sec "МАРШРУТЫ ПОСЛЕ ПОДКЛЮЧЕНИЯ"
run ip route show
run ip -6 route show

sec "DNS ПОСЛЕ ПОДКЛЮЧЕНИЯ"
run resolvectl status awg0 --no-pager
run cat /etc/resolv.conf

# ── самое важное: было ли рукопожатие ───────────────────────────────────────
# Раньше здесь читался UAPI из unix-сокета отдельного процесса amneziawg-go.
# Такого процесса больше нет: ядро подключено к backend'у библиотекой, и всё,
# что оно знает о туннеле, backend отдаёт в статусе.
sec "СОСТОЯНИЕ ТУННЕЛЯ — был ли хендшейк"
curl -s -m 5 "http://127.0.0.1:$DIAG_PORT/api/vpn/status" | python3 -c '
import json, sys, datetime

try:
    st = json.load(sys.stdin)
except Exception as e:
    print("не удалось прочитать статус:", e)
    raise SystemExit

print("состояние:      ", st.get("state"))
print("конфигурация:   ", st.get("config_name") or "—")
print("интерфейс:      ", st.get("interface") or "—")
print("принято/отдано: ", st.get("bytes_received"), "/", st.get("bytes_sent"))

handshake = st.get("last_handshake")
if not handshake:
    print("рукопожатие:     ХЕНДШЕЙКА НЕ БЫЛО")
else:
    when = datetime.datetime.fromisoformat(handshake.replace("Z", "+00:00"))
    ago = (datetime.datetime.now(when.tzinfo) - when).total_seconds()
    print(f"рукопожатие:     {handshake} ({int(ago)} сек назад)")

if st.get("error"):
    print("ошибка:         ", st["error"])
' >> "$REPORT" 2>&1

sec "ПРОВЕРКА СВЯЗИ ЧЕРЕЗ ТУННЕЛЬ"
run ping -c 3 -W 3 1.1.1.1
run ping -c 3 -W 3 -M do -s 1200 1.1.1.1
run getent hosts example.com
echo "" >> "$REPORT"
echo "\$ curl -4 https://example.com" >> "$REPORT"
curl -4 -s -o /dev/null -m 15 -w "HTTP %{http_code} за %{time_total}s\n" https://example.com/ >> "$REPORT" 2>&1 \
    || echo "ПРОВАЛ (нет соединения)" >> "$REPORT"

sec "ЛОГ BACKEND"
rep ""
cat "$BACKEND_LOG" >> "$REPORT" 2>&1

# вырезаем всё, что похоже на ключи
sed -i -E 's/(private_key|preshared_key|header_protection_key|PrivateKey|PresharedKey|HeaderProtectionKey)([=: ]+)[A-Za-z0-9+/=]{8,}/\1\2<вырезано>/g' "$REPORT"

rm -f "$BACKEND_LOG"
ok "Сбор данных закончен"
