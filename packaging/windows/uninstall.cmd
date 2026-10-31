@echo off
chcp 65001 >nul
rem Удаление AWG Client. Конфигурации в %ProgramData%\awg-client остаются.
setlocal
set "DEST=%ProgramFiles%\AWG Client"
net session >nul 2>&1
if errorlevel 1 (
  echo Запустите от имени администратора.
  pause
  exit /b 1
)
if exist "%DEST%\awg-ui.exe" "%DEST%\awg-ui.exe" uninstall
if exist "%DEST%\awg-service.exe" "%DEST%\awg-service.exe" uninstall
del "%PUBLIC%\Desktop\AWG Client.url" >nul 2>&1
echo Службы удалены. Папку "%DEST%" теперь можно удалить.
echo Конфигурации остались в %ProgramData%\awg-client
pause
