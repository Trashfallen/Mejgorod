# Mejgorod

[Русская версия](README.ru.md)

One-click Windows VPN client on the [mihomo](https://github.com/MetaCubeX/mihomo) core. Paste the same config you use in XKeen on your router, press the power button, and the whole PC goes through mihomo in TUN mode. Lives in the system tray.

## Features

- **One button.** Connect and disconnect from the main screen or the tray icon. The main screen also has a server dropdown (with "Add server") and a profile dropdown.
- **Profiles.** Several configs side by side: your own, "Everything through VPN", "Selective" and so on. A template becomes a profile when picked, then it is edited like your own. Profiles are managed on the Config tab.
- **Router config as is.** The XKeen config is adapted for the PC on the fly. Your text is never rewritten.
- **Templates.** The editor starts empty. The "Template" button loads a built-in config or any `.yaml` from the `templates` folder next to the exe.
- **Servers.** Import `vless://` links (the link is picked up from the clipboard) and switch between them. The first imported server is selected automatically. The gear button opens the server parameters in YAML (SNI, fingerprint, flow, transport...), checked by the core before saving.
- **Subscriptions.** Paste a subscription link in the same import dialog and the app fetches it - base64 or plain text lists of links, or a raw mihomo/clash config with a `proxies:` block. Found servers show as a checklist (everything checked by default) so you pick which ones to keep.
- **Groups board.** Two columns, "Via VPN" and "Direct". Drag a service (YouTube, Discord, ...) between them or click it to pick a specific server. Works with the VPN off too: the choice is applied on connect.
- **Your own sites and apps.** The "+" button in a column adds a site or a program. For a site, its domains are looked up in the MetaCubeX geosite/geoip lists (instagram.com -> 74 domains, auto-updated). For a program, all its traffic goes the chosen way (`PROCESS-NAME`).
- **Logs and checks.** Core logs with filters. The config is validated by the core before start, and errors jump to the line in the editor.
- **Works next to Citrix.** Coexists with Citrix Secure Access (see below).
- **Updates.** On start the app checks the releases of this repository. "Update" downloads the new exe, swaps it and restarts without a UAC prompt, the window stays open.
- **Light.** While the window is hidden, it barely polls. The config is re-read only when it changes, and the connection list is fetched from the core only while someone is looking.
- **Portable.** Everything is stored next to the exe. The mihomo core is downloaded from the official releases on the first connect.

## Requirements

- Windows 10 or 11, x64.
- Administrator rights: TUN cannot be created without them. The app asks for elevation on start.

## Getting started

1. Build the app (see [Building](#building)) or take `Mejgorod.exe` from the Releases page, if there is one.
2. Put `Mejgorod.exe` in any folder and run it. Data is kept in `data` next to the exe.
3. On the "Config" tab, paste your config or load a template.
4. If the config has no servers, import a `vless://` link or a subscription link on the "Servers" tab.
5. Press the power button.

Closing the window hides the app to the tray and keeps the VPN running. To quit, use the tray menu or "Settings" -> "Quit".

## Templates

- **Selective VPN** (built in): blocked services and per-service groups go through the VPN, everything else is `MATCH,DIRECT`.
- **Everything through VPN, Russian sites direct** (built in): a single PROXY group, `MATCH,PROXY`.
- Any `.yaml` from the `templates` folder next to the exe.

The templates contain no servers. Keep personal configs with UUIDs in `templates`, not in the exe, and do not share that folder.

## What happens to the config

The config of the active profile is stored as pasted (`data\profiles\<name>.yaml`). Before starting the core, the app builds a PC version of it (`data\home\config.yaml`):

- router-only settings are commented out: `redir-port`, `tproxy-port`, `routing-mark`, `interface-name`;
- `allow-lan: false`, `external-controller: 127.0.0.1:<port>`;
- `find-process-mode: off` becomes `strict`, so `PROCESS-NAME` rules work;
- `listen` is removed from `dns`, a `tun` block is added;
- if the PC has no IPv6, `ipv6: false` is set;
- server addresses are excluded from TUN (`route-exclude-address`);
- servers from the "Servers" tab are added as the inline proxy provider `mihomodesk`, so groups with `include-all: true` see them.

Removed lines are commented out, not deleted, so line numbers in core errors match the editor.

Sites and programs added with "+" are written into the config itself, so the same config works on the router too:

```yaml
rules:
  # Mejgorod: свои сайты и программы (вкладка «Группы»)
  - OR,((DOMAIN-SUFFIX,instagram.com),(RULE-SET,instagram@domain)),PROXY # desk: instagram.com
  - PROCESS-NAME,Telegram.exe,DIRECT # desk: Telegram.exe
  # Mejgorod: конец
```

The section goes first in `rules`, so an explicit choice wins over the other rules. Lists found for sites go into a similar section at the end of `rule-providers`. Do not edit lines between the markers by hand: the Groups tab rewrites them. Profiles saved before the app was renamed from MihomoDesk keep working: their old `# MihomoDesk: ...` markers are still recognized and get migrated to the new ones on the next edit.

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

The script embeds the icon and version info and writes `dist\Mejgorod.exe`.

UI debugging without admin rights and without TUN:

```
set MEJGOROD_DEV_NOTUN=1
dist\Mejgorod.exe --no-elevate
```

Tests: `go test ./...`. Checking a real config with the core:

```
set MEJGOROD_CONFIG=path\to\config.yaml
set MEJGOROD_CORE=dist\data\core\mihomo.exe
go test -run TestAdaptRealConfig -v .
```

## Releases

The app updates itself from the latest release of this repository:

1. Run `release.cmd 0.2.0` (the next version). It builds `release\Mejgorod.exe` and `release\Mejgorod-0.2.0.zip` (exe + mihomo core + a short guide).
2. Create a release with the tag `v0.2.0` and attach both files. The exe must be named exactly `Mejgorod.exe`: the app downloads it when updating. The zip is for people installing from scratch.
3. Bump the default version in `main.go` and `build.cmd`.

Apps with an older version offer the update on start. The version only goes up: a release with a lower number is never offered.

## Credits

- [mihomo](https://github.com/MetaCubeX/mihomo) by MetaCubeX: the proxy core, downloaded from its official releases.
- [zashboard](https://github.com/Zephyruso/zashboard): the web dashboard added to the config.
- [fyne.io/systray](https://github.com/fyne-io/systray): the tray icon.
