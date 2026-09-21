@echo off
rem Build MihomoDesk.exe: icon and version info in resources, no console window.
setlocal
cd /d "%~dp0"
go run ./cmd/genicon winres || exit /b 1
go run github.com/tc-hib/go-winres@latest simply --icon winres/icon.png --manifest gui --arch amd64 ^
  --product-name MihomoDesk --file-description "MihomoDesk VPN (mihomo core)" ^
  --product-version 1.0.0 --file-version 1.0.0 --original-filename MihomoDesk.exe --out rsrc || exit /b 1
go build -trimpath -ldflags "-H=windowsgui -s -w" -o dist\MihomoDesk.exe . || exit /b 1
echo Done: dist\MihomoDesk.exe
