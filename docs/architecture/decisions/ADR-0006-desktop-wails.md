# ADR-0006: Wails for the desktop application

**Status:** Accepted
**Date:** 2026-08-29
**Implements:** [Phase 9](../../ROADMAP.md#phase-9--desktop)

## Context

Most people will not use a VPN through a terminal. Headnet needs a desktop
application for Windows, macOS and Linux with a tray icon, connection status,
sign-in and diagnostics.

The application has to reach the client daemon, which is where all the
networking and key material lives.

## Decision

**Wails**: Go backend, Svelte frontend, using the platform's native WebView.

The desktop UI is a **client of the local daemon API**. It implements no
networking logic.

```
Desktop UI (Wails)
      ↓  local IPC
Client daemon (headnetd)      ← the only privileged component
      ↓  HTTPS
Control plane
      ↓
WireGuard
```

## Alternatives

**Electron.** The most common choice, and rejected on size and resource use. A
VPN tray application that idles at 200 MB of RAM is not acceptable on a
laptop, and bundling Chromium for a status window is disproportionate.

**Tauri.** Genuinely close, and technically excellent — also a native WebView,
also small. Rejected because the backend is Rust, which would introduce a
second language for shared code the daemon already implements in Go. If
Headnet were a Rust project this would be the answer.

**Native per platform** (WinUI, SwiftUI, GTK). The best result, at three times
the work and three codebases to keep in agreement. Not justified for what is
mostly a status window and a settings pane.

**A local web UI in the browser.** Tempting, since the web UI already exists.
Rejected because there is no tray icon, no autostart, and no obvious place for
connection status — and because a VPN client that lives in a browser tab is
easy to close by accident.

## Consequences

**Good.**

- Small binaries, low memory, no bundled browser.
- Go on the backend, so it shares packages with the daemon and the server.
- Svelte on the frontend, so it shares components and conventions with the
  existing web UI.
- Native tray support on all three platforms.

**Bad.**

- WebView behaviour differs between platforms (WebKit on macOS, WebView2 on
  Windows, WebKitGTK on Linux). CSS and JavaScript differences do surface.
- Linux requires WebKitGTK to be installed.
- A smaller community than Electron or Tauri, so fewer answers when something
  goes wrong.

**Constraints this imposes.**

- **The UI must never become the source of truth for network state.** It
  displays what the daemon reports. Anything else produces a UI that says
  "connected" while the tunnel is down — the exact dishonesty this project
  refuses elsewhere.
- **The UI is unprivileged.** All privileged operations go through the daemon,
  so a bug in the UI cannot become a root compromise.
- The daemon's local API has to be designed as a real interface, because it
  has at least two consumers (the CLI and this) and eventually third-party
  ones.
