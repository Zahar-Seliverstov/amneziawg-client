@echo off
chcp 65001 >nul
rem Установка AWG Client. Запускать от имени администратора.
rem Копирует программу в %ProgramFiles%\AWG Client, ставит две службы:
rem awg-service (туннель и API) и awg-ui (интерфейс на http://127.0.0.1:7070)
rem и ярлык на рабочий стол. Повторный запуск обновляет установку.
setlocal
set "DEST=%ProgramFiles%\AWG Client"

net session >nul 2>&1
if errorlevel 1 (
  echo Запустите от имени администратора: правая кнопка - "Запуск от имени администратора".
  pause
  exit /b 1
)

rem Службы останавливаем до замены файлов: занятый exe не перезаписать.
if exist "%DEST%\awg-ui.exe" "%DEST%\awg-ui.exe" uninstall
if exist "%DEST%\awg-service.exe" "%DEST%\awg-service.exe" uninstall

rem Не из папки распаковки: служба интерфейса работает под LocalService и
rem не может читать файлы из профиля пользователя (Загрузки, Рабочий стол).
robocopy "%~dp0." "%DEST%" /MIR /NFL /NDL /NJH /NJS /NP >nul
if errorlevel 8 goto :fail

"%DEST%\awg-service.exe" install || goto :fail
"%DEST%\awg-ui.exe" install || goto :fail

(
  echo [InternetShortcut]
  echo URL=http://127.0.0.1:7070/
  echo IconIndex=0
  echo IconFile=%DEST%\awg-client.ico
) > "%PUBLIC%\Desktop\AWG Client.url"

echo.
echo Готово. Интерфейс: http://127.0.0.1:7070
echo Удаление: "%DEST%\uninstall.cmd" от имени администратора.
start "" http://127.0.0.1:7070
pause
exit /b 0

:fail
echo Установка не удалась.
pause
exit /b 1
