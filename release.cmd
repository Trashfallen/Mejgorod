@echo off
rem Build a release into the release folder:
rem   release\Mejgorod.exe          - for the in-app update (attach to the GitHub release as is)
rem   release\Mejgorod-VERSION.zip  - for people: exe + mihomo core + short guide
rem Usage: release.cmd 0.1.3
setlocal
cd /d "%~dp0"
if "%~1"=="" (
  echo Usage: release.cmd VERSION
  exit /b 1
)
set "VERSION=%~1"
if not exist dist\data\core\mihomo.exe (
  echo dist\data\core\mihomo.exe not found: connect once in dist\Mejgorod.exe to download the core
  exit /b 1
)
call "%~dp0build.cmd" release\Mejgorod.exe || exit /b 1
if exist release\pkg rmdir /s /q release\pkg
mkdir release\pkg\Mejgorod\data\core || exit /b 1
copy /y release\Mejgorod.exe release\pkg\Mejgorod\ >nul || exit /b 1
copy /y dist\data\core\mihomo.exe release\pkg\Mejgorod\data\core\ >nul || exit /b 1
copy /y package\*.txt release\pkg\Mejgorod\ >nul || exit /b 1
powershell -NoProfile -Command "Compress-Archive -Path 'release\pkg\Mejgorod' -DestinationPath 'release\Mejgorod-%VERSION%.zip' -Force" || exit /b 1
rmdir /s /q release\pkg
echo.
echo Release %VERSION% is ready:
echo   release\Mejgorod.exe
echo   release\Mejgorod-%VERSION%.zip
