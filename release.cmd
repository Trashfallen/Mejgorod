@echo off
rem Build a release into its own folder release\vVERSION with only what goes to GitHub:
rem   Mejgorod.exe          - for the in-app update (attach as is)
rem   Mejgorod-VERSION.zip  - for people: exe + mihomo core + short guide
rem Usage: release.cmd 0.2.3
setlocal
cd /d "%~dp0"
if "%~1"=="" (
  echo Usage: release.cmd VERSION
  exit /b 1
)
set "VERSION=%~1"
set "OUT=release\v%VERSION%"
set "STAGE=%TEMP%\mejgorod-stage-%VERSION%"
if not exist dist\data\core\mihomo.exe (
  echo dist\data\core\mihomo.exe not found: connect once in dist\Mejgorod.exe to download the core
  exit /b 1
)
if exist "%OUT%" rmdir /s /q "%OUT%"
mkdir "%OUT%" || exit /b 1
call "%~dp0build.cmd" "%OUT%\Mejgorod.exe" || exit /b 1
if exist "%STAGE%" rmdir /s /q "%STAGE%"
mkdir "%STAGE%\Mejgorod\data\core" || exit /b 1
copy /y "%OUT%\Mejgorod.exe" "%STAGE%\Mejgorod\" >nul || exit /b 1
copy /y dist\data\core\mihomo.exe "%STAGE%\Mejgorod\data\core\" >nul || exit /b 1
copy /y package\*.txt "%STAGE%\Mejgorod\" >nul || exit /b 1
powershell -NoProfile -Command "Compress-Archive -Path '%STAGE%\Mejgorod' -DestinationPath '%OUT%\Mejgorod-%VERSION%.zip' -Force" || exit /b 1
rmdir /s /q "%STAGE%"
echo.
echo Release %VERSION% is ready, upload everything from %OUT%\:
dir /b "%OUT%"
