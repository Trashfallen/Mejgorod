#!/usr/bin/env bash
# Перезапуск отладочной копии: выход старой, сборка, старт без TUN и без UAC.
set -e
cd "$(dirname "$0")"
if [ -f dist/data/instance.json ]; then
  port=$(python -c "import json;print(json.load(open('dist/data/instance.json'))['port'])")
  tok=$(python -c "import json;print(json.load(open('dist/data/instance.json'))['token'])")
  curl -s -m 3 -X POST -H "X-Desk-Token: $tok" "http://127.0.0.1:$port/api/quit" >/dev/null || true
  for i in $(seq 1 50); do [ -f dist/data/instance.json ] || break; sleep 0.2; done
fi
go build -trimpath -ldflags "-H=windowsgui -s -w" -o dist/MihomoDesk.exe .
(cd dist && MIHOMODESK_DEV_NOTUN=1 ./MihomoDesk.exe --no-elevate --autostart >/dev/null 2>&1 &)
for i in $(seq 1 50); do [ -f dist/data/instance.json ] && break; sleep 0.2; done
python -c "import json;d=json.load(open('dist/data/instance.json'));print('http://127.0.0.1:%d/#t=%s'%(d['port'],d['token']))"
