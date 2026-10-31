# AWG Client

Клиент [AmneziaWG](https://docs.amnezia.org/) для **Linux** и **Windows**.
Служба поднимает туннель, интерфейс открывается в браузере:
<http://127.0.0.1:7070>. Ядро `amneziawg-go` встроено — ставить отдельно
ничего не нужно.

- подключения из файлов `.conf` AmneziaWG;
- маршрутизация: через VPN только список или всё, кроме списка;
- правила по домену, IP, подсети, наборам `geosite:`/`geoip:` и программам;
- автоподключение при старте системы и переподключение при обрыве.

**Содержание:** [Установка](#установка) · [Первый запуск](#первый-запуск) ·
[Правила](#правила-маршрутизации) · [Управление службой](#управление-службой) ·
[Обновление](#обновление) · [Удаление](#удаление) ·
[Неполадки](#если-что-то-не-работает) · [Сборка](#сборка-из-исходников)

---

## Установка

Архивы — на [странице релизов][releases], под x86_64.

### Linux

Нужны systemd и iproute2 (есть почти везде).

```sh
# скачать последнюю версию
v=$(curl -fsSL https://api.github.com/repos/Zahar-Seliverstov/amneziawg-client/releases/latest \
    | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')
curl -fLO "https://github.com/Zahar-Seliverstov/amneziawg-client/releases/download/v$v/awg-client_${v}_linux_amd64.tar.gz"

# распаковать и установить
tar xzf "awg-client_${v}_linux_amd64.tar.gz"
sudo awg-client/install.sh
```

Программа ставится в `/usr/local/lib/awg-client`, в меню приложений
появляется «AWG Client».

### Windows 10/11

В **PowerShell** (обычном, не от администратора):

```powershell
# скачать последнюю версию
$v = (Invoke-RestMethod https://api.github.com/repos/Zahar-Seliverstov/amneziawg-client/releases/latest).tag_name.TrimStart('v')
Invoke-WebRequest "https://github.com/Zahar-Seliverstov/amneziawg-client/releases/download/v$v/awg-client_${v}_windows_amd64.zip" -OutFile awg-client.zip

# распаковать и установить (Windows спросит права администратора)
Expand-Archive awg-client.zip -DestinationPath awg-client -Force
Start-Process 'awg-client\AWG Client\install.cmd' -Verb RunAs
```

Или руками: скачать `awg-client_<версия>_windows_amd64.zip`, распаковать,
правой кнопкой по `install.cmd` → «Запуск от имени администратора».

Программа ставится в `C:\Program Files\AWG Client`, на рабочем столе
появляется ярлык «AWG Client».

### Проверка контрольных сумм

Скачайте `SHA256SUMS` из того же релиза.

```sh
# Linux
sha256sum -c SHA256SUMS --ignore-missing
```

```powershell
# Windows: сравнить со строкой из SHA256SUMS
(Get-FileHash awg-client.zip -Algorithm SHA256).Hash.ToLower()
```

---

## Первый запуск

1. Откройте <http://127.0.0.1:7070> (или ярлык «AWG Client»).
2. **Подключения → + Добавить**: вставьте текст `.conf` или нажмите
   «Открыть файл…».
3. **Подключить** — кнопка справа в строке состояния (или клавиша `C`).

Строка состояния показывает сервер, время в туннеле, пинг и трафик.
Автоподключение включается во вкладке **Настройки**.

| Клавиша | Действие |
|---|---|
| `/` | перейти к полю правил |
| `C` | подключить или отключить |
| `,` | настройки |
| `Esc` | назад, закрыть подсказки |
| `Ctrl+Enter` | сохранить форму |

---

## Правила маршрутизации

Сначала выберите режим:

- **только список** — через VPN идут правила из списка, остальное напрямую;
- **всё, кроме списка** — через VPN идёт всё, правила из списка — напрямую.

Правило вписывается в одно поле, вид определяется по значению. Можно
вставить сразу список — по одному на строку или через пробел.

| Пример | Что охватывает |
|---|---|
| `example.com` | домен со всеми поддоменами |
| `.example` | вся зона: всё, что кончается на `.example` |
| `full:www.example.com` | ровно это имя, без поддоменов |
| `keyword:example` | все имена, где есть это слово |
| `regexp:^cdn[0-9]+\.` | имена по регулярному выражению |
| `203.0.113.10` | один адрес |
| `198.51.100.0/24` | подсеть |
| `geosite:youtube` | сайты категории из базы geosite.dat |
| `geoip:nl` | сети страны из базы geoip.dat |
| `app:firefox` | все соединения программы (только Linux) |
| `app:discord/udp` | только UDP программы: голос, видео (только Linux) |

Слово без точки (`youtube`) ищет категорию в базах и подсказывает её.
Весь список можно править текстом («Править текстом»), выгрузить и загрузить
файлом JSON или списком строк («Экспорт» / «Импорт…»).

Базы `geosite.dat` и `geoip.dat` служба скачивает и обновляет сама раз в
неделю; вручную — во вкладке **Настройки**.

---

## Управление службой

Работают две службы: **`awg-service`** (туннель, маршруты, API на
`127.0.0.1:7071`) и **`awg-ui`** (интерфейс на `127.0.0.1:7070`). Обе
стартуют вместе с системой.

### Linux

```sh
systemctl status awg-service awg-ui        # состояние
sudo systemctl restart awg-service         # перезапуск (туннель переподключится)
journalctl -u awg-service -f               # журнал в реальном времени
journalctl -u awg-service --since -1h      # журнал за последний час
curl -s http://127.0.0.1:7071/api/vpn/status   # состояние туннеля из API
```

### Windows (PowerShell)

```powershell
Get-Service awg-service, awg-ui            # состояние
Restart-Service awg-service                # перезапуск (PowerShell от администратора)

# журнал службы туннеля (PowerShell от администратора)
Get-Content "$env:ProgramData\awg-client\logs\awg-service.log" -Tail 50 -Wait

# состояние туннеля из API
Invoke-RestMethod http://127.0.0.1:7070/api/vpn/status
```

Где что лежит:

| | Linux | Windows |
|---|---|---|
| Программа | `/usr/local/lib/awg-client` | `C:\Program Files\AWG Client` |
| Подключения и правила | `/var/lib/awg-client` | `%ProgramData%\awg-client` |
| Журналы | `journalctl -u awg-service` | `%ProgramData%\awg-client\logs` |

Папки с данными доступны только администраторам: в `.conf` лежат ключи.

---

## Обновление

Скачайте новый архив и установите поверх — команды те же, что при
[установке](#установка). Подключения и правила сохраняются.

---

## Удаление

```sh
# Linux
sudo /usr/local/lib/awg-client/install.sh --uninstall
```

```powershell
# Windows (PowerShell)
Start-Process "$env:ProgramFiles\AWG Client\uninstall.cmd" -Verb RunAs
```

Подключения и правила остаются в `/var/lib/awg-client` или
`%ProgramData%\awg-client` — удалите папку вручную, если они не нужны.

---

## Если что-то не работает

**Интерфейс пишет «Нет связи со службой».** Служба `awg-service` не
запущена — посмотрите её состояние и журнал (см.
[Управление службой](#управление-службой)).

**«Подключено», но сайты не открываются.**

- Не включён ли второй VPN-клиент. Его блокировка трафика (kill switch)
  режет чужие туннели, а два туннеля на один сервер мешают друг другу.
  Оставьте включённым один.
- Режим «только список»: через VPN идёт только список, остальное — напрямую.
  Сайт, недоступный без VPN, нужно добавить в список (например,
  `geosite:youtube`) или переключиться на «всё, кроме списка».

**Пинг показывает «—».** Сервер не отвечает ни на ICMP, ни на TCP — на
работу туннеля это не влияет.

**Порт 7070 или 7071 занят.** Проверьте, кто его держит:

```sh
ss -ltnp | grep -E ':707[01]'                       # Linux
```

```powershell
Get-NetTCPConnection -LocalPort 7070,7071 -State Listen   # Windows
```

---

## Сборка из исходников

Нужны Go 1.25+, Node.js 22+, `make`, `curl`, `python3`.

```sh
git clone https://github.com/Zahar-Seliverstov/amneziawg-client.git
cd amneziawg-client

./install.sh      # Linux: собрать и установить
make dist         # архивы для Linux и Windows в dist/
make test         # go vet под обе ОС, тесты Go и интерфейса
```

Windows-архив собирается на Linux кросс-компиляцией; `wintun.dll`
скачивается с wintun.net со сверкой контрольной суммы.

### Разработка

```sh
make dev-service   # служба (через sudo) с отдельным состоянием в backend/build/dev
make dev-frontend  # интерфейс с горячей перезагрузкой на :5173, /api — в службу
make dev-ui        # собранный интерфейс через awg-ui на :7070
```

### Как устроено

- **`backend/cmd/awg-service`** — ядро, маршруты, DNS, состояние и HTTP-API.
  Права администратора: root под systemd, LocalSystem в Windows.
- **`backend/cmd/awg-ui`** — без прав (DynamicUser / LocalService): раздаёт
  интерфейс и пересылает `/api` службе, поэтому CORS не нужен.
- **`frontend/src`** — Vue: `api/` (клиент и типы), `lib/` (чистые функции с
  тестами), `stores/` (общее состояние), `composables/`, `components/`.

Платформенные части разделены файлами `_unix.go` / `_windows.go`:

| Что | Linux | Windows |
|---|---|---|
| TUN | `/dev/net/tun` | wintun |
| Адреса и маршруты (`internal/iproute`) | команда `ip` | IP Helper API |
| Серверы имён (`internal/vpn/dns_*`) | resolvconf или `/etc/resolv.conf` | DNS интерфейса туннеля |
| Запуск службой (`internal/service`) | юнит systemd | служба Windows |

Оба порта слушают только петлевой адрес; от чужих страниц в браузере API
закрыт проверкой `Host` и `Origin` (`internal/localguard`).

### Выпуск релиза

Версия — в файле `VERSION`, она должна совпадать с тегом.

```sh
# поднять VERSION, описать изменения в CHANGELOG.md, закоммитить
git tag -a v2.1.0 -m "AWG Client 2.1.0"
git push origin main v2.1.0
```

[Workflow](.github/workflows/release.yml) соберёт архивы для Linux и
Windows, посчитает контрольные суммы и опубликует релиз с заметками из
`CHANGELOG.md`.

[releases]: https://github.com/Zahar-Seliverstov/amneziawg-client/releases/latest
