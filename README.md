# AmneziaWG Client

Десктопный клиент AmneziaWG для Linux: оболочка на Tauri, backend на Go со
встроенным ядром `amneziawg-go`, интерфейс на Nuxt. Ядро подключено
библиотекой, отдельный бинарник ставить не нужно.

## Установка

Готовые пакеты — на [странице релизов][releases]. Собраны под x86_64.

```sh
# Debian, Ubuntu и производные
sudo apt install ./awg-client_<версия>_amd64.deb

# Любой дистрибутив
chmod +x awg-client_<версия>_amd64.AppImage
./awg-client_<версия>_amd64.AppImage
```

Файлы можно сверить с `SHA256SUMS` из того же релиза:

```sh
sha256sum -c SHA256SUMS --ignore-missing
```

[releases]: https://github.com/Zahar-Seliverstov/amneziawg-client/releases/latest

## Требования для сборки

- Go 1.25+
- Node.js 20+
- Rust (cargo, rustc)
- `make`

## Сборка и установка из исходников

```sh
./install.sh
```

Скрипт соберёт фронтенд, вошьёт его в backend, соберёт оболочку и поставит
приложение в `~/.local`. Пароль администратора спрашивается один раз — для
копии backend'а в `/usr/local`, которая позволяет запускать VPN без запроса
пароля при каждом старте.

Запуск: «AWG Client» в меню приложений.

## Разработка

```sh
make build          # собрать всё
make dev-frontend   # Nuxt в режиме разработки
make dev-backend    # backend отдельно (требует root)
```

Backend поднимает API и веб-интерфейс на одном порту `127.0.0.1:8081`.

## Выпуск релиза

Версия объявлена в трёх манифестах и должна совпадать с тегом, иначе сборка в
CI остановится: `desktop/package.json`, `desktop/src-tauri/Cargo.toml` (и
`Cargo.lock`), `desktop/src-tauri/tauri.conf.json`. Больше её нигде вписывать
не нужно: backend получает номер из `tauri.conf.json` на сборке, а интерфейс
запрашивает его у backend'а.

```sh
# 1. Поднять версию в манифестах и описать изменения в CHANGELOG.md
# 2. Закоммитить, затем:
git tag -a v1.2.3 -m "AWG Client 1.2.3"
git push origin main v1.2.3
```

Дальше всё делает [workflow](.github/workflows/release.yml): собирает `.deb`
и AppImage, считает контрольные суммы и публикует релиз с заметками из
соответствующего раздела `CHANGELOG.md`.

## Удаление

```sh
./install.sh --uninstall
```

Конфигурации в `~/.config/awg-client` остаются нетронутыми.
