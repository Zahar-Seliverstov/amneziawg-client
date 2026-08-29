#!/bin/bash
#
# AmneziaWG Web Client — единая точка запуска.
#
#   ./start.sh
#
# Запускать ОБЫЧНЫМ пользователем, без sudo.
# Пароль спросит один раз: root нужен только backend'у для управления сетью
# (TUN-интерфейс, маршруты, DNS). Сборка и frontend идут от пользователя,
# поэтому root-овые файлы в проекте больше не появляются.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKEND_DIR="$SCRIPT_DIR/backend"
FRONTEND_DIR="$SCRIPT_DIR/frontend"
BACKEND_BIN="$BACKEND_DIR/build/awg-client"

BACKEND_PORT="${BACKEND_PORT:-8081}"
FRONTEND_PORT="${FRONTEND_PORT:-3000}"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'
log_info()    { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[OK]${NC} $1"; }
log_warn()    { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error()   { echo -e "${RED}[ERROR]${NC} $1"; }
die()         { log_error "$1"; exit 1; }

BACKEND_PID=""
FRONTEND_PID=""
SUDO_KEEP_PID=""

# ─── Если запустили через sudo — вернуться к обычному пользователю ───────────
if [ "${EUID:-$(id -u)}" -eq 0 ]; then
    if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != "root" ]; then
        echo -e "${YELLOW}[WARN]${NC} Запущено через sudo — перезапускаю от $SUDO_USER."
        exec sudo -u "$SUDO_USER" -H -- "$0" "$@"
    fi
    die "Не запускай этот скрипт от root. Просто: ./start.sh"
fi

CONFIG_DIR="$HOME/.config/awg-client"
CONFIG_FILE="$CONFIG_DIR/config.json"

# ─── Возврат прав на файлы, которые мог создать root ─────────────────────────
restore_ownership() {
    local targets=("$SCRIPT_DIR")
    [ -d "$CONFIG_DIR" ] && targets+=("$CONFIG_DIR")

    if [ -n "$(find "${targets[@]}" -user root -print -quit 2>/dev/null)" ]; then
        log_info "Возвращаю права на файлы..."
        sudo -n chown -R "$(id -u):$(id -g)" "${targets[@]}" 2>/dev/null
    fi
}

# ─── Остановка всего ─────────────────────────────────────────────────────────
cleanup() {
    trap - INT TERM EXIT
    echo ""
    log_info "Остановка сервисов..."

    if [ -n "$FRONTEND_PID" ]; then
        pkill -TERM -P "$FRONTEND_PID" 2>/dev/null
        kill -TERM "$FRONTEND_PID" 2>/dev/null
    fi

    # backend работает от root — гасим его через sudo, чтобы он успел
    # корректно отключить VPN и убрать маршруты
    sudo -n pkill -TERM -f "^$BACKEND_BIN" 2>/dev/null

    local waited=0
    while [ "$waited" -lt 10 ] && sudo -n pgrep -f "^$BACKEND_BIN" >/dev/null 2>&1; do
        sleep 1; waited=$((waited + 1))
    done
    sudo -n pkill -KILL -f "^$BACKEND_BIN" 2>/dev/null

    [ -n "$SUDO_KEEP_PID" ] && kill "$SUDO_KEEP_PID" 2>/dev/null
    jobs -p | xargs -r kill 2>/dev/null

    restore_ownership
    log_success "Готово"
    exit 0
}
trap cleanup INT TERM EXIT

# ─── Проверка зависимостей ───────────────────────────────────────────────────
check_dependencies() {
    log_info "Проверка зависимостей..."
    local missing=()
    for c in go node npm curl sudo; do
        command -v "$c" &>/dev/null || missing+=("$c")
    done
    [ ${#missing[@]} -ne 0 ] && die "Не хватает: ${missing[*]}"

    [ -e /dev/net/tun ] || log_warn "Нет /dev/net/tun — VPN не подключится (sudo modprobe tun)"

    local awg_found=""
    for p in /opt/AmneziaVPN/bin/amneziawg-go /usr/local/bin/amneziawg-go /usr/bin/amneziawg-go; do
        [ -x "$p" ] && { awg_found="$p"; break; }
    done
    [ -z "$awg_found" ] && command -v amneziawg-go &>/dev/null && awg_found="$(command -v amneziawg-go)"
    [ -z "$awg_found" ] && log_warn "amneziawg-go не найден — интерфейс поднимется, VPN нет"

    log_success "Зависимости в порядке"
}

# ─── Один запрос пароля на весь сеанс ────────────────────────────────────────
acquire_sudo() {
    if ! sudo -n true 2>/dev/null; then
        log_info "Нужен пароль sudo (root требуется backend'у для сети)"
        sudo -v || die "Без sudo backend не сможет управлять VPN"
    fi
    # держим sudo-тикет живым, пока работает скрипт
    ( while true; do
        sleep 50
        kill -0 "$$" 2>/dev/null || exit
        sudo -n true 2>/dev/null || exit
      done ) &
    SUDO_KEEP_PID=$!
}

# ─── Чиним права, оставшиеся от прошлых запусков под sudo ────────────────────
fix_stale_permissions() {
    if [ -n "$(find "$SCRIPT_DIR" -user root -print -quit 2>/dev/null)" ]; then
        log_warn "Найдены файлы от root (прошлый запуск под sudo) — чиню права"
        sudo -n chown -R "$(id -u):$(id -g)" "$SCRIPT_DIR" || die "Не удалось сменить владельца"
        rm -rf "$FRONTEND_DIR/.nuxt" "$FRONTEND_DIR/.output"
    fi
    if [ -d "$CONFIG_DIR" ] && [ -n "$(find "$CONFIG_DIR" -user root -print -quit 2>/dev/null)" ]; then
        sudo -n chown -R "$(id -u):$(id -g)" "$CONFIG_DIR"
    fi
}

# ─── Останавливаем backend, оставшийся от прошлого запуска ───────────────────
kill_stale_backend() {
    if sudo -n pgrep -f "^$BACKEND_BIN" >/dev/null 2>&1; then
        log_warn "Backend от прошлого запуска ещё жив — останавливаю"
        sudo -n pkill -TERM -f "^$BACKEND_BIN" 2>/dev/null
        sleep 2
        sudo -n pkill -KILL -f "^$BACKEND_BIN" 2>/dev/null
    fi
}

# ─── Сборка backend (от пользователя) ────────────────────────────────────────
build_backend() {
    log_info "Сборка backend..."
    cd "$BACKEND_DIR" || die "Нет каталога $BACKEND_DIR"

    export GOCACHE="$BACKEND_DIR/.cache/go-build"
    export GOMODCACHE="$BACKEND_DIR/.cache/go-mod"
    export GOFLAGS="${GOFLAGS:-} -mod=mod"
    export GOSUMDB=off
    mkdir -p build .cache

    local out
    if ! out="$(go build -o "$BACKEND_BIN" ./cmd/awg-client 2>&1)"; then
        echo "$out"
        die "Backend не собрался"
    fi
    log_success "Backend собран"
}

# ─── Зависимости frontend ────────────────────────────────────────────────────
setup_frontend() {
    cd "$FRONTEND_DIR" || die "Нет каталога $FRONTEND_DIR"
    export npm_config_cache="$FRONTEND_DIR/.npm-cache"
    export NUXT_TELEMETRY_DISABLED=1
    mkdir -p "$npm_config_cache"

    if [ ! -d node_modules ] || [ package.json -nt node_modules ]; then
        log_info "Установка npm-пакетов (может занять пару минут)..."
        if ! npm install --no-fund --no-audit >/dev/null 2>&1; then
            die "npm install упал — запусти вручную: cd frontend && npm install"
        fi
    fi
    log_success "Frontend готов"
}

# ─── Ждём, пока сервис реально ответит по HTTP ───────────────────────────────
wait_for_http() {
    local url="$1" timeout="$2" pid="$3" name="$4" waited=0
    while [ "$waited" -lt "$timeout" ]; do
        if [ -n "$pid" ] && ! kill -0 "$pid" 2>/dev/null; then
            log_error "$name упал при запуске"
            return 1
        fi
        if curl -sf -o /dev/null --max-time 3 "$url"; then
            return 0
        fi
        sleep 1
        waited=$((waited + 1))
    done
    log_error "$name не ответил за ${timeout}с"
    return 1
}

# ─── Запуск backend от root ──────────────────────────────────────────────────
start_backend() {
    log_info "Запуск backend на http://127.0.0.1:$BACKEND_PORT ..."
    cd "$BACKEND_DIR" || return 1

    # HOME и -config задаём явно: иначе под sudo конфиг уедет в /root
    sudo -n env HOME="$HOME" "$BACKEND_BIN" \
        -host 127.0.0.1 \
        -port "$BACKEND_PORT" \
        -config "$CONFIG_FILE" &
    BACKEND_PID=$!

    wait_for_http "http://127.0.0.1:$BACKEND_PORT/api/vpn/status" 20 "$BACKEND_PID" "Backend" \
        || die "Backend не поднялся"
    log_success "Backend работает (root, VPN доступен)"
}

# ─── Запуск frontend от пользователя ─────────────────────────────────────────
start_frontend() {
    log_info "Запуск frontend на http://127.0.0.1:$FRONTEND_PORT ..."
    cd "$FRONTEND_DIR" || return 1

    export NUXT_TELEMETRY_DISABLED=1
    export npm_config_cache="$FRONTEND_DIR/.npm-cache"

    # --host обязателен: без него Nuxt слушает только [::1],
    # и IPv4-клиенты (в т.ч. проверка ниже) до него не достучатся
    npm run dev -- --host 127.0.0.1 --port "$FRONTEND_PORT" &
    FRONTEND_PID=$!

    wait_for_http "http://127.0.0.1:$FRONTEND_PORT/" 120 "$FRONTEND_PID" "Frontend" \
        || die "Frontend не поднялся"
    log_success "Frontend работает"
}

# ─── main ────────────────────────────────────────────────────────────────────
main() {
    echo ""
    echo -e "${GREEN}╔══════════════════════════════════════════╗${NC}"
    echo -e "${GREEN}║     AmneziaWG Web Client - Запуск        ║${NC}"
    echo -e "${GREEN}╚══════════════════════════════════════════╝${NC}"
    echo ""

    check_dependencies
    acquire_sudo
    fix_stale_permissions
    kill_stale_backend
    build_backend
    setup_frontend

    echo ""
    start_backend
    start_frontend

    echo ""
    echo -e "${GREEN}════════════════════════════════════════════${NC}"
    echo -e "${GREEN}  Всё запущено${NC}"
    echo ""
    echo -e "  ${BLUE}Интерфейс:${NC}   http://127.0.0.1:$FRONTEND_PORT"
    echo -e "  ${BLUE}Backend API:${NC} http://127.0.0.1:$BACKEND_PORT/api"
    echo ""
    echo -e "  ${YELLOW}Ctrl+C${NC} — остановить всё"
    echo -e "${GREEN}════════════════════════════════════════════${NC}"
    echo ""

    if command -v xdg-open &>/dev/null; then
        ( xdg-open "http://127.0.0.1:$FRONTEND_PORT" >/dev/null 2>&1 & )
    fi

    wait
}

main "$@"
