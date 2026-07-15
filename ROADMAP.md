# Roadmap

Where Discord Tunnel is, and what stands between it and an app you could hand to
someone who has never opened a terminal.

## Done — the core is proven

Verified end-to-end on a censoring network (Türk Telekom, which blocks Discord
and drops UDP):

- Discord's traffic — and only Discord's — travels the server.
- DNS poisoning is defeated: Discord resolves to real Cloudflare IPs, not the
  ISP sinkhole.
- **Voice works.** UDP is carried through the tunnel; the `--smoke` check proves
  it by confirming UDP exits from the server's IP, and a real call confirmed it.

Everything below is about turning a proven core into a finished product.

---

## M1 — Reliability: safe as a daily driver

The tunnel works; now it has to keep working while you forget it exists.

- [ ] **Kill-switch (leak protection).** Today, while the watchdog reconnects
      after a drop, the TUN adapter is gone and Discord's traffic briefly takes
      the normal route — straight into the block. On a call that is a 3-second
      dropout, and on a censoring network it is a leak of the very traffic we
      hide. Add a "block, don't leak" mode: hold a route that null-routes the
      tunnelled processes while the tunnel is down, released only when it is back.
- [ ] **Single-instance guard.** Two copies fight over the adapter (observed).
      A named mutex; a second launch focuses the first instead of starting.
- [ ] **Clean adapter teardown before recreate.** Restarting too fast races
      Windows releasing `tun0` ("open interface take too much time"). Wait for
      the adapter to actually disappear before recreating, with a bounded retry.
- [ ] **Sleep/resume and network change.** Confirm the tunnel recovers after the
      laptop wakes or Wi-Fi↔Ethernet switches; drive it from the watchdog rather
      than hoping `auto_detect_interface` catches it.
- [ ] **Log rotation.** `tunnel.log` grows without bound; cap and roll it.
- [ ] **Silence reverse-DNS noise.** PTR lookups for local IPs spam the log with
      timeouts against the router. Disable reverse mapping / tune the sniffer.

## M2 — First run and settings without a text editor

Right now the server is configured by hand-editing `config.json`. That is fine
for the author and a wall for everyone else.

- [ ] **First-run window.** Paste a `vless://` link or fill in the fields; the
      app writes `config.json` and connects. No JSON.
- [ ] **`vless://` link parser.** One paste instead of six fields.
- [ ] **Settings UI.** Server, which apps to tunnel, autostart, connect-on-launch
      — all from the tray, not a file.
- [ ] **Richer tray.** Live latency, time connected, last error in plain words;
      a notification when the tunnel drops or recovers.

## M3 — Distribution: something you can give away

- [ ] **Installer** (NSIS or MSI): bundles `wintun.dll`, Start Menu shortcut,
      proper uninstaller, installs to Program Files.
- [ ] **Code signing.** Unsigned, SmartScreen shows "Windows protected your PC"
      and UAC says "unknown publisher" — trust-killing. A signing cert, or at
      minimum an honest README section explaining the warning.
- [ ] **GitHub Releases on tag.** CI already builds; have it attach a signed
      `.exe` + installer to a release when a `vX.Y.Z` tag is pushed.
- [ ] **Self-update.** Check a manifest, download, swap in place, relaunch.
- [ ] **`--version`.** Embed the build version; surface it in the tray.

## M4 — Robustness and polish

- [ ] **Launch Discord de-elevated.** The app runs elevated for the adapter, so
      Discord inherits admin (drag-and-drop from Explorer breaks). Launch it at
      normal integrity by duplicating the shell's token.
- [ ] **Explicit `discord_path` in config.** Override auto-detection for unusual
      installs, instead of relying on the scan.
- [ ] **Multiple servers / failover.** A list, with automatic fall-over when one
      stops answering.
- [ ] **Diagnostics bundle.** One click produces a shareable, secret-scrubbed
      report (versions, adapter state, recent log) for bug reports.
- [ ] **More tests.** Tray state transitions and the tunnel lifecycle
      (start/stop/rebuild) are currently untested.

## M5 — Beyond Discord

- [ ] **Tunnel other blocked apps.** The engine is process-generic; expose it.
      Add/remove processes from the tray, not the config.
- [ ] **Per-app split-tunnel UI.** See what is tunnelled and toggle it.
- [ ] **Traffic stats.** Data carried, per app, this session.

---

## Suggested order

**M1 first**, and within it the kill-switch first of all: the one place the app
is currently *unsafe* rather than merely unfinished is the leak window during a
reconnect. After M1 the app is trustworthy to run every day. M2 makes it usable
by someone else; M3 makes it giveable; M4/M5 are refinement.
