@echo off
chcp 65001 >nul
setlocal

if "%BILI_SCROLL_SERVER%"=="" set "BILI_SCROLL_SERVER=http://127.0.0.1:8788"
if "%BILI_SCROLL_TOKEN%"=="" (
  echo Set BILI_SCROLL_TOKEN to a device token created in Bili Scroll Agent.
  pause
  exit /b 2
)

cd /d "%~dp0..\.."
bin\bili-scroll-usage-sync.exe --watch --server "%BILI_SCROLL_SERVER%"
pause
