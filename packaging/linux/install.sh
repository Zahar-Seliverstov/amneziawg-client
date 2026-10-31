#!/bin/sh
#
# Установка AWG Client из готового архива. Запускать от root:
#
#   sudo ./install.sh              установить или обновить
#   sudo ./install.sh --uninstall  удалить (конфигурации остаются)
#
# Ставит две службы systemd: awg-service (туннель и API, от root) и awg-ui
# (интерфейс на http://127.0.0.1:7070, без прав), и ярлык в меню приложений.
set -eu

PREFIX=/usr/local/lib/awg-client
APPS=/usr/share/applications
ICONS=/usr/share/icons/hicolor/128x128/apps
DATA=/var/lib/awg-client
HERE="$(cd "$(dirname "$0")" && pwd)"

[ "$(id -u)" -eq 0 ] || { echo "Нужны права root: sudo $0 $*" >&2; exit 1; }
command -v systemctl >/dev/null || { echo "Нужен systemd" >&2; exit 1; }

if [ "${1:-}" = "--uninstall" ]; then
    [ -x "$PREFIX/awg-ui" ] && "$PREFIX/awg-ui" uninstall || true
    [ -x "$PREFIX/awg-service" ] && "$PREFIX/awg-service" uninstall || true
    rm -rf "$PREFIX"
    rm -f "$APPS/awg-client.desktop" "$ICONS/awg-client.png"
    echo "Удалено. Конфигурации остались в $DATA"
    exit 0
fi

# Конфигурации прежней, десктопной версии лежали в доме пользователя.
# Переносим их один раз — если у службы своих ещё нет.
migrate() {
    [ -e "$DATA/config.json" ] && return
    user="${SUDO_USER:-}"
    [ -n "$user" ] || return
    home="$(getent passwd "$user" | cut -d: -f6)"
    old="$home/.config/awg-client/config.json"
    [ -f "$old" ] || return
    install -d -m 700 "$DATA"
    install -m 600 "$old" "$DATA/config.json"
    echo "Конфигурации перенесены из $old"
}

# Остатки десктопной версии (Tauri): оболочка в доме пользователя, её
# автозапуск и копия службы под polkit. Новая служба их заменяет.
cleanup_desktop() {
    # Оболочку гасим первой: её служба, увидев это, сама разберёт туннель.
    pkill -f "lib/awg-client/awg-client-desktop" 2>/dev/null || true
    sleep 2
    pkill -f "^$PREFIX/awg-client -socket" 2>/dev/null || true
    rm -f "$PREFIX/awg-client" /usr/share/polkit-1/actions/org.amnezia.awgclient.policy
    user="${SUDO_USER:-}"
    [ -n "$user" ] || return 0
    home="$(getent passwd "$user" | cut -d: -f6)"
    [ -n "$home" ] || return 0
    rm -rf "$home/.local/lib/awg-client"
    rm -f "$home/.local/bin/awg-client-desktop" \
          "$home/.local/share/applications/awg-client.desktop" \
          "$home/.config/autostart/awg-client.desktop" \
          "$home"/.local/share/icons/hicolor/*/apps/awg-client.png
}

command -v ip >/dev/null || echo "Внимание: нет команды ip (iproute2) — без неё туннель работать не будет" >&2

cleanup_desktop

# Службы останавливаем до замены файлов: занятый бинарник не перезаписать.
systemctl stop awg-ui awg-service 2>/dev/null || true

install -d "$PREFIX"
install -m 755 "$HERE/awg-service" "$HERE/awg-ui" "$HERE/install.sh" "$PREFIX/"
rm -rf "$PREFIX/www"
cp -r "$HERE/www" "$PREFIX/www"
chmod -R a+rX "$PREFIX/www"

migrate

"$PREFIX/awg-service" install
"$PREFIX/awg-ui" install

install -Dm644 "$HERE/awg-client.png" "$ICONS/awg-client.png"
install -Dm644 "$HERE/awg-client.desktop" "$APPS/awg-client.desktop"
command -v update-desktop-database >/dev/null && update-desktop-database "$APPS" 2>/dev/null || true
command -v gtk-update-icon-cache >/dev/null && gtk-update-icon-cache -qtf /usr/share/icons/hicolor 2>/dev/null || true

echo
echo "Готово. Интерфейс: http://127.0.0.1:7070 (или «AWG Client» в меню)"
