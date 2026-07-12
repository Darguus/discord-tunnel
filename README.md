# Discord Tunnel

A tiny Windows tray app that routes **one application** — Discord — through your
own server, and leaves everything else on your machine alone.

It exists because app-level proxying (`--proxy-server=socks5://…`) only ever
carries TCP, so Discord **voice**, which is UDP, escapes the proxy entirely. On
a network that blocks UDP that means voice silently doesn't work. Discord Tunnel
captures traffic at a virtual network adapter instead, so voice travels the
tunnel like everything else.

- **Split tunnel by process.** Only `Discord.exe` (and its updater) go through
  the server. Your browser, your games, your downloads take the normal route.
- **Voice works**, because UDP is carried inside the connection, not dropped.
- **No launch flags, no updater workaround.** Discord runs as an ordinary app.
- **VLESS + REALITY.** The connection is indistinguishable from a normal TLS
  handshake to a real website, using the engine from
  [sing-box](https://github.com/SagerNet/sing-box).
- **One binary.** A tray icon, a colour, a three-item menu. That's the whole UI.

> Built for legitimate access to a service you are entitled to use, on your own
> server. You are responsible for complying with the terms of any network and
> service you use it with.

## How it works

```
                 ┌──────────────────────────────────────────────┐
   Discord.exe ──┤ TUN adapter → routing → VLESS/REALITY → your ─┼──▶ VPS ──▶ Discord
   (TCP + UDP)   │                    │                          │
   everything ───┤                    └── (no match) ── direct ──┼──▶ normal internet
   else          └──────────────────────────────────────────────┘
```

1. A virtual network adapter (via [wintun](https://www.wintun.net)) captures all
   traffic on the machine — the only way Windows lets a program see another
   process's UDP.
2. A routing rule matches `Discord.exe` by process name and sends it to the
   VLESS outbound; DNS for Discord is resolved *through* the tunnel so it can't
   be poisoned locally.
3. Everything that matches no rule falls through to `direct` and leaves via your
   real connection, untouched. That fall-through is what makes it a *split*
   tunnel rather than a whole-machine VPN.

The translation from your config into a sing-box config lives in one file,
[`internal/singbox/generate.go`](internal/singbox/generate.go), and is covered
by tests that feed the generated document through sing-box's own decoder — so a
broken config fails in CI, not on your machine mid-call.

## Install

1. Download `DiscordTunnel.exe` and `wintun.dll` from a
   [release](../../releases), or build from source (below). Keep the two files
   together.
2. Create your config. If you already use Xray or Amnezia, import it:
   ```powershell
   .\DiscordTunnel.exe --import-xray "C:\path\to\xray\config.json"
   ```
   Otherwise, run it once to write a starter `config.json` and fill in the
   `server` section by hand (see [`config.example.json`](config.example.json)).
   The file lives in `%APPDATA%\DiscordTunnel\config.json`.
3. Check it, then verify the tunnel actually reaches Discord:
   ```powershell
   .\DiscordTunnel.exe --check
   .\DiscordTunnel.exe --smoke      # run from an Administrator terminal
   ```
4. Launch it. It asks for administrator rights once (needed to create the
   adapter) and drops to the tray.

## Using it

The tray icon is the whole app:

| Colour | Meaning |
|--------|---------|
| Grey   | Off |
| Amber  | Connecting or reconnecting |
| Green  | Connected — traffic confirmed flowing through the server |
| Red    | Error (bad config, or elevation declined) |

Right-click for the menu: connect/disconnect, launch Discord, **Start with
Windows**, and open the log or config. "Start with Windows" registers a
scheduled task that starts the tunnel silently at logon — no UAC prompt each
time, unlike a normal startup entry for an elevated app.

If the connection drops, a watchdog re-dials on its own; you'll see the icon go
amber and back to green without touching anything.

## Configuration

`%APPDATA%\DiscordTunnel\config.json`:

```jsonc
{
  "server": {
    "address": "your.server.com",
    "port": 443,
    "uuid": "…",                       // your VLESS user id
    "flow": "xtls-rprx-vision",
    "reality": {
      "server_name": "www.googletagmanager.com",  // the site your server borrows
      "public_key": "…",               // 43-char REALITY public key
      "short_id": "…",                 // up to 16 hex chars
      "fingerprint": "chrome"
    }
  },
  "tunnel": {
    "processes": ["Discord.exe", "Update.exe"],   // matched by process name
    "domains": [],                     // optional: also tunnel these by suffix
    "log_level": "warn",
    "mtu": 1420
  },
  "app": {
    "connect_on_launch": true
  }
}
```

`domains` is an escape hatch for the rare case where some Discord traffic comes
from a process you don't control (a browser tab, a game overlay). Leave it empty
unless you need it.

## Build from source

Requires Go 1.24+ and Windows.

```powershell
.\build.ps1 -Release
```

The build **must** include sing-box's feature tags — `with_utls` (REALITY),
`with_gvisor` (the network stack) and `with_quic` — or the binary compiles but
fails to connect. `build.ps1` sets them; if you build by hand:

```powershell
go build -tags "with_gvisor,with_utls,with_quic" -ldflags "-H=windowsgui -s -w" ./cmd/discord-tunnel
```

Run the tests (they validate the generated sing-box config against sing-box's
own parser):

```powershell
go test -tags "with_gvisor,with_utls,with_quic" ./...
```

## Layout

| Path | What |
|------|------|
| `cmd/discord-tunnel` | Entry point, flags, elevation |
| `internal/config`    | The user-facing config: schema, validation, Xray import |
| `internal/singbox`   | Translation into a sing-box config — the routing brain |
| `internal/tunnel`    | Lifecycle: start/stop, the reconnect watchdog, the probe |
| `internal/smoke`     | `--smoke`: bring the tunnel up and confirm Discord is reachable |
| `internal/winenv`    | Windows plumbing: elevation, autostart, finding Discord |
| `internal/tray`      | The tray UI |
| `tools/genicons`     | Regenerates the tray icons |

## License

MIT. The wintun driver is redistributed under its own license (see
[wintun.net](https://www.wintun.net)); it is not part of this repository.
Networking by [sing-box](https://github.com/SagerNet/sing-box).
