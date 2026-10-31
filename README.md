# AmneziaWG Client

Клиент AmneziaWG для Linux и Windows с веб-интерфейсом. Служба на Go со
встроенным ядром `amneziawg-go` поднимает туннель, интерфейс на Vue + Vite
открывается в браузере. Ядро подключено библиотекой, отдельный бинарник
ставить не нужно.

## Интерфейс

Одна страница в стиле инструментов разработчика: строка состояния (время в
туннеле, пинг, трафик), подключения слева, правила маршрутизации справа.
Клавиши: `/` — поле правил, `c` — подключить или отключить, `,` — настройки.

Список правил идёт через VPN или мимо него, вид правила определяется по самому
значению. «Мимо VPN» — это «как без VPN»: если у системы есть свой путь (второй
туннель, TUN прокси), трафик идёт им. Программу целиком можно отправить мимо
туннеля правилом `app:minecraft` (только Linux).

## Установка

Готовые архивы — на [странице релизов][releases]. Собраны под x86_64.

**Linux** (нужны systemd и iproute2):

```sh
tar xzf awg-client_<версия>_linux_amd64.tar.gz
sudo awg-client/install.sh
```

**Windows 10/11**: распаковать `awg-client_<версия>_windows_amd64.zip` и
запустить `install.cmd` от имени администратора. Программа копируется в
`C:\Program Files\AWG Client`, на рабочем столе появляется ярлык.

После установки интерфейс открыт на <http://127.0.0.1:7070>. Обе службы
стартуют вместе с системой.

Файлы можно сверить с `SHA256SUMS` из того же релиза:

```sh
sha256sum -c SHA256SUMS --ignore-missing
```

[releases]: https://github.com/Zahar-Seliverstov/amneziawg-client/releases/latest

## Сборка из исходников

Нужны Go 1.25+, Node.js 20+, `make`, `curl` и `python3`.

```sh
./install.sh      # Linux: собрать и установить
make dist         # архивы для Linux и Windows в dist/
```

Windows-архив собирается на Linux кросс-компиляцией; `wintun.dll`
скачивается с wintun.net со сверкой контрольной суммы.

## Разработка

```sh
make dev-service   # служба (через sudo) с отдельным состоянием в backend/build/dev
make dev-frontend  # интерфейс с горячей перезагрузкой на :5173, /api — в службу
make dev-ui        # или собранный интерфейс через awg-ui на :7070
make test          # go vet под обе ОС, тесты Go и тесты интерфейса
```

### Как устроено

Два процесса, как у Tailscale и Mullvad, только вместо окна — браузер:

- **`awg-service`** (`backend/cmd/awg-service`) — ядро, маршруты, DNS,
  состояние и HTTP-API на `127.0.0.1:7071`. Работает с правами
  администратора: root под systemd, LocalSystem как служба Windows.
  Состояние — в `/var/lib/awg-client` или `%ProgramData%\awg-client`,
  доступ только администраторам;
- **`awg-ui`** (`backend/cmd/awg-ui`) — без прав (DynamicUser в systemd,
  LocalService в Windows). Раздаёт собранный интерфейс (`frontend/`) на
  `127.0.0.1:7070` и пересылает `/api` службе. Страница и API живут на
  одном адресе, поэтому CORS не нужен.

Платформенные части спрятаны за интерфейсами и разделены файлами
`_unix.go` / `_windows.go`:

| Что | Linux | Windows |
|---|---|---|
| TUN | `/dev/net/tun` | wintun |
| Адреса и маршруты (`internal/iproute`) | команда `ip` | IP Helper API |
| Серверы имён (`internal/vpn/dns_*`) | resolvconf или `/etc/resolv.conf` | DNS интерфейса туннеля + метрика 0 |
| Запуск службой (`internal/service`) | юнит systemd | служба Windows |

Оба порта слушают только петлевой адрес. От чужих страниц в браузере API
закрыт проверкой `Host` и `Origin` (`internal/localguard`): это защищает от
подделки запросов и от подмены DNS. От других пользователей той же машины
он не закрыт — открыть интерфейс может любой, кто сидит за компьютером.

Проверить API вручную:

```sh
curl http://127.0.0.1:7071/api/vpn/status
```

## Выпуск релиза

Версия объявлена в одном месте — файле `VERSION` — и должна совпадать с
тегом, иначе сборка в CI остановится. Служба получает номер на сборке, а
интерфейс запрашивает его у службы.

```sh
# 1. Поднять версию в VERSION и описать изменения в CHANGELOG.md
# 2. Закоммитить, затем:
git tag -a v1.2.3 -m "AWG Client 1.2.3"
git push origin main v1.2.3
```

Дальше всё делает [workflow](.github/workflows/release.yml): собирает
архивы для Linux и Windows, считает контрольные суммы и публикует релиз с
заметками из соответствующего раздела `CHANGELOG.md`.

## Удаление

- Linux: `sudo /usr/local/lib/awg-client/install.sh --uninstall`
- Windows: `C:\Program Files\AWG Client\uninstall.cmd` от имени администратора

Конфигурации остаются в `/var/lib/awg-client` или `%ProgramData%\awg-client`.
