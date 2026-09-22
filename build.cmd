@echo off
rem Build MihomoDesk.exe: icon and version info in resources, no console window.
rem Usage: build.cmd [output.exe]   (default dist\MihomoDesk.exe)
setlocal
cd /d "%~dp0"
set "OUT=%~1"
if "%OUT%"=="" set "OUT=dist\MihomoDesk.exe"
rem App version: "set VERSION=1.2.1" before running, default below
if "%VERSION%"=="" set "VERSION=1.2.0"
go run ./cmd/genicon winres || exit /b 1
go run github.com/tc-hib/go-winres@latest simply --icon winres/icon.png --manifest gui --arch amd64 ^
  --product-name MihomoDesk --file-description "MihomoDesk VPN (mihomo core)" ^
  --product-version %VERSION% --file-version %VERSION% --original-filename MihomoDesk.exe --out rsrc || exit /b 1
go build -trimpath -ldflags "-H=windowsgui -s -w -X main.appVersion=%VERSION%" -o "%OUT%" . || exit /b 1
echo Done: %OUT% (%VERSION%)
