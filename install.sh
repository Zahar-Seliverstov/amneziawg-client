#!/bin/sh
#
# Сборка из исходников и установка на Linux.
#
#   ./install.sh              собрать и установить (или обновить)
#   ./install.sh --uninstall  удалить (конфигурации остаются)
#
# Запускать обычным пользователем: пароль администратора спросит sudo —
# службы ставятся в систему. Нужны Go 1.25+, Node.js 20+, make и systemd.
set -eu
cd "$(dirname "$0")"

if [ "${1:-}" = "--uninstall" ]; then
    if [ -x /usr/local/lib/awg-client/install.sh ]; then
        exec sudo /usr/local/lib/awg-client/install.sh --uninstall
    fi
    exec sudo packaging/linux/install.sh --uninstall
fi

for c in make go node npm; do
    command -v "$c" >/dev/null || { echo "Не хватает: $c" >&2; exit 1; }
done

make dist-linux
sudo dist/linux/awg-client/install.sh
