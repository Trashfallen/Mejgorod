# MihomoDesk

[Русская версия](README.ru.md)

One-click Windows VPN client on the [mihomo](https://github.com/MetaCubeX/mihomo) core. Paste the same config you use in XKeen on your router, press the power button, and the whole PC goes through mihomo in TUN mode. Lives in the system tray.

## Features

- **One button.** Connect and disconnect from the main screen or the tray icon.
- **Router config as is.** The XKeen config is adapted for the PC on the fly. Your text is never rewritten.
- **Templates.** The editor starts empty. The "Template" button loads a built-in config or any `.yaml` from the `templates` folder next to the exe.
- **Servers.** Import `vless://` links (the link is picked up from the clipboard) and switch between them. The first imported server is selected automatically.
- **Groups.** Pick a server for each service group and check the latency.
- **Logs and checks.** Core logs with filters. The config is validated by the core before start, and errors jump to the line in the editor.
- **Works next to Citrix.** Coexists with Citrix Secure Access (see below).
- **Portable.** Everything is stored next to the exe. The mihomo core is downloaded from the official releases on the first connect.

## Requirements

- Windows 10 or 11, x64.
- Administrator rights: TUN cannot be created without them. The app asks for elevation on start.

## Getting started

1. Build the app (see [Building](#building)) or take `MihomoDesk.exe` from the Releases page, if there is one.
2. Put `MihomoDesk.exe` in any folder and run it. Data is kept in `data` next to the exe.
3. On the "Config" tab, paste your config or load a template.
4. If the config has no servers, import a `vless://` link on the "Servers" tab.
5. Press the power button.

Closing the window hides the app to the tray and keeps the VPN running. To quit, use the tray menu or "Settings" -> "Quit".

## Templates

- **Selective VPN** (built in): blocked services and per-service groups go through the VPN, everything else is `MATCH,DIRECT`.
- **Everything through VPN, Russian sites direct** (built in): a single PROXY group, `MATCH,PROXY`.
- Any `.yaml` from the `templates` folder next to the exe.

The templates contain no servers. Keep personal configs with UUIDs in `templates`, not in the exe, and do not share that folder.

## What happens to the config

The config is stored as pasted (`data\config.yaml`). Before starting the core, the app builds a PC version of it (`data\home\config.yaml`):

- router-only settings are commented out: `redir-port`, `tproxy-port`, `routing-mark`, `interface-name`;
- `allow-lan: false`, `external-controller: 127.0.0.1:<port>`;
- `find-process-mode: off` becomes `strict`, so `PROCESS-NAME` rules work;
- `listen` is removed from `dns`, a `tun` block is added;
- if the PC has no IPv6, `ipv6: false` is set;
- server addresses are excluded from TUN (`route-exclude-address`);
- servers from the "Servers" tab are added as the inline proxy provider `mihomodesk`, so groups with `include-all: true` see them.

Removed lines are commented out, not deleted, so line numbers in core errors match the editor.

## Next to Citrix Secure Access

Citrix intercepts outgoing packets, puts "foreign" ones back into the Windows stack and resets connections it does not like. A regular TUN with a default route cannot live with it. The core catches its own connections (thousands of `reject loopback`, the PC slows down), and Citrix routes its own gateway through TUN and breaks itself.

While Citrix is connected, the "Auto" route mode (Settings -> TUN -> Routes) switches to compatibility mode:

- DNS gives apps fake-ip addresses. Only those go to TUN, plus subnets from `no-resolve` rules that lead to the VPN (for example, OVH for game servers, Telegram, Meta). Everything else, including the core's own connections, bypasses TUN;
- Citrix internal domains (the DNS search list) get real addresses from the Citrix DNS;
- the Citrix gateway and the servers are excluded from TUN;
- the TUN stack is forced to gvisor, because Citrix resets packets of the system and mixed stacks.

If Citrix connects or disconnects while the VPN is on, the app reconnects by itself. If a routing loop still happens, the VPN turns off within a few seconds, and core log output is capped at 100 lines per second.

## Building

Requires Go 1.25+.

```
build.cmd
```

The script embeds the icon and version info and writes `dist\MihomoDesk.exe`.

UI debugging without admin rights and without TUN:

```
set MIHOMODESK_DEV_NOTUN=1
dist\MihomoDesk.exe --no-elevate
```

Tests: `go test ./...`. Checking a real config with the core:

```
set MIHOMODESK_CONFIG=path\to\config.yaml
set MIHOMODESK_CORE=dist\data\core\mihomo.exe
go test -run TestAdaptRealConfig -v .
```

## Credits

- [mihomo](https://github.com/MetaCubeX/mihomo) by MetaCubeX: the proxy core, downloaded from its official releases.
- [zashboard](https://github.com/Zephyruso/zashboard): the web dashboard added to the config.
- [fyne.io/systray](https://github.com/fyne-io/systray): the tray icon.
