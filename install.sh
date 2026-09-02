#!/bin/bash
#
# AmneziaWG Web Client — установка и обновление десктопного приложения.
#
#   ./install.sh              собрать и установить (обновляет и root-копию backend'а)
#   ./install.sh --no-root    только пользовательская часть, ничего системного
#   ./install.sh --uninstall  удалить установленное
#
# Запускать ОБЫЧНЫМ пользователем. Пароль администратора спрашивается один
# раз и только ради копии backend'а в /usr/local: именно её оболочка
# запускает без запроса пароля при каждом старте.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR" || exit 1

# Должно совпадать с Makefile: скрипт лишь вызывает его цели.
DESKTOP_PREFIX="${DESKTOP_PREFIX:-$HOME/.local}"
DESKTOP_LIBDIR="$DESKTOP_PREFIX/lib/awg-client"
PRIV_LIBDIR="/usr/local/lib/awg-client"
PRIV_POLICY="/usr/share/polkit-1/actions/org.amnezia.awgclient.policy"
BACKEND_BIN="$SCRIPT_DIR/backend/build/awg-client"
POLICY_SRC="$SCRIPT_DIR/desktop/org.amnezia.awgclient.policy"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; BLUE='\033[0;34m'; NC='\033[0m'
log_info()    { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[OK]${NC} $1"; }
log_warn()    { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error()   { echo -e "${RED}[ERROR]${NC} $1"; }
die()         { log_error "$1"; exit 1; }

WITH_ROOT=1
MODE="install"

for arg in "$@"; do
    case "$arg" in
        --no-root)   WITH_ROOT=0 ;;
        --uninstall) MODE="uninstall" ;;
        -h|--help)
            sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
            exit 0 ;;
        *) die "Неизвестный ключ: $arg (см. --help)" ;;
    esac
done

if [ "${EUID:-$(id -u)}" -eq 0 ]; then
    die "Не запускай от root: приложение ставится в $DESKTOP_PREFIX, пароль спросят сами команды."
fi

# ─── Запуск команды с правами root ───────────────────────────────────────────
# В терминале хватает sudo. Без tty (скрипт дёрнули из графики или из другого
# инструмента) пароль спрашивает polkit-агент через pkexec.
run_as_root() {
    if sudo -n true 2>/dev/null; then
        sudo "$@"
    elif [ -t 0 ]; then
        sudo "$@"
    elif command -v pkexec >/dev/null 2>&1; then
        pkexec "$@"
    else
        return 1
    fi
}

# ─── Проверка зависимостей ───────────────────────────────────────────────────
check_dependencies() {
    log_info "Проверка зависимостей..."
    local missing=()
    for c in make go node npm cargo rustc; do
        command -v "$c" &>/dev/null || missing+=("$c")
    done
    [ ${#missing[@]} -ne 0 ] && die "Не хватает: ${missing[*]}"
    log_success "Всё на месте"
}

# ─── Предупреждения о запущенных копиях ──────────────────────────────────────
warn_running() {
    if pgrep -x awg-client-desktop >/dev/null 2>&1; then
        log_warn "Приложение сейчас запущено — новая версия заработает после его перезапуска"
    fi
}

# ─── Сборка и установка в домашний каталог ───────────────────────────────────
install_user_part() {
    log_info "Сборка и установка в $DESKTOP_LIBDIR (несколько минут)..."
    make desktop-install || die "Сборка не удалась"
    log_success "Приложение установлено"
}

# ─── Копия backend'а под root ────────────────────────────────────────────────
# Без неё polkit спрашивает пароль при КАЖДОМ запуске приложения. И её нужно
# обновлять вместе с кодом: оболочка предпочитает системную копию всем
# остальным, поэтому устаревшая молча откатывает backend на старую версию.
install_privileged_part() {
    [ -x "$BACKEND_BIN" ] || die "Нет собранного backend'а: $BACKEND_BIN"

    if [ -f "$PRIV_POLICY" ]; then
        log_info "Обновляю системную копию backend'а (нужен пароль администратора)..."
    else
        log_info "Ставлю системную копию backend'а — после неё пароль при запуске больше не спросят..."
    fi

    if ! run_as_root /bin/sh -c "
        install -Dm755 -o root -g root '$BACKEND_BIN' '$PRIV_LIBDIR/awg-client' &&
        install -Dm644 -o root -g root '$POLICY_SRC' '$PRIV_POLICY'
    "; then
        log_warn "Системная копия не обновлена."
        log_warn "Приложение будет запускать УСТАРЕВШИЙ backend из $PRIV_LIBDIR."
        log_warn "Повтори позже: ./install.sh  (или сними разрешение: make desktop-nopasswd-off)"
        return 1
    fi

    log_success "Системная копия обновлена"
}

# ─── Удаление ────────────────────────────────────────────────────────────────
uninstall() {
    log_info "Удаляю приложение из $DESKTOP_PREFIX..."
    make desktop-uninstall || log_warn "make desktop-uninstall завершился с ошибкой"

    if [ -f "$HOME/.config/autostart/awg-client.desktop" ]; then
        rm -f "$HOME/.config/autostart/awg-client.desktop"
        log_info "Ярлык автозапуска удалён"
    fi

    if [ "$WITH_ROOT" -eq 1 ] && { [ -e "$PRIV_POLICY" ] || [ -e "$PRIV_LIBDIR" ]; }; then
        log_info "Убираю системную копию backend'а (нужен пароль администратора)..."
        run_as_root /bin/sh -c "rm -f '$PRIV_POLICY'; rm -rf '$PRIV_LIBDIR'" \
            || log_warn "Системную копию убрать не удалось — она осталась в $PRIV_LIBDIR"
    fi

    log_success "Удалено. Конфигурации в ~/.config/awg-client не тронуты."
}

# ─── main ────────────────────────────────────────────────────────────────────
main() {
    echo ""
    echo -e "${GREEN}╔══════════════════════════════════════════╗${NC}"
    if [ "$MODE" = "uninstall" ]; then
        echo -e "${GREEN}║     AWG Client - Удаление                ║${NC}"
    else
        echo -e "${GREEN}║     AWG Client - Установка               ║${NC}"
    fi
    echo -e "${GREEN}╚══════════════════════════════════════════╝${NC}"
    echo ""

    if [ "$MODE" = "uninstall" ]; then
        uninstall
        exit 0
    fi

    check_dependencies
    warn_running
    install_user_part

    local priv_ok=1
    if [ "$WITH_ROOT" -eq 1 ]; then
        install_privileged_part || priv_ok=0
    else
        log_info "Системная копия backend'а не тронута (--no-root)"
        [ -f "$PRIV_POLICY" ] && log_warn "В $PRIV_LIBDIR осталась прежняя копия — приложение запустит именно её"
    fi

    echo ""
    echo -e "${GREEN}════════════════════════════════════════════${NC}"
    echo -e "${GREEN}  Готово${NC}"
    echo ""
    echo -e "  ${BLUE}Запуск:${NC}     «AWG Client» в меню приложений"
    echo -e "  ${BLUE}Или:${NC}        $DESKTOP_PREFIX/bin/awg-client-desktop"
    [ "$priv_ok" -eq 0 ] && echo -e "  ${YELLOW}Внимание:${NC}   системная копия backend'а осталась старой"
    echo -e "${GREEN}════════════════════════════════════════════${NC}"
    echo ""
}

main "$@"
