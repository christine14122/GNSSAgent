@echo off
setlocal

powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0build.ps1"
set "BUILD_EXIT_CODE=%ERRORLEVEL%"
if not "%BUILD_EXIT_CODE%"=="0" exit /b %BUILD_EXIT_CODE%

powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0build-hf.ps1"
set "BUILD_EXIT_CODE=%ERRORLEVEL%"
if not "%BUILD_EXIT_CODE%"=="0" exit /b %BUILD_EXIT_CODE%

echo Build completed successfully.
exit /b 0
