@echo off
rem Build MihomoDesk.exe: icon and version info in resources, no console window.
rem Usage: build.cmd [output.exe]   (default dist\MihomoDesk.exe)
setlocal
cd /d "%~dp0"
set "OUT=%~1"
if "%OUT%"=="" set "OUT=dist\MihomoDesk.exe"
go run ./cmd/genicon winres || exit /b 1
go run github.com/tc-hib/go-winres@latest simply --icon winres/icon.png --manifest gui --arch amd64 ^
  --product-name MihomoDesk --file-description "MihomoDesk VPN (mihomo core)" ^
  --product-version 1.2.0 --file-version 1.2.0 --original-filename MihomoDesk.exe --out rsrc || exit /b 1
go build -trimpath -ldflags "-H=windowsgui -s -w" -o "%OUT%" . || exit /b 1
echo Done: %OUT%
