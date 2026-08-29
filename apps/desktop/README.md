# Desktop application

**Status: not implemented.** Planned for
[Phase 9](../../docs/ROADMAP.md#phase-9--desktop).

This directory is a placeholder. It contains no code, deliberately — an empty
directory that looks like a component is worse than an honest note.

## What will go here

A tray application for Windows, macOS and Linux, built with
[Wails](https://wails.io/) — Go and Svelte over the platform's native WebView.

It talks to the local client daemon and implements no networking logic of its
own. Two rules follow from that:

- **The UI is never the source of truth for network state.** It displays what
  the daemon reports. Anything else produces a UI that says "connected" while
  the tunnel is down.
- **The UI is unprivileged.** All privileged operations go through the daemon,
  so a bug here cannot become a root compromise.

See [ADR-0006](../../docs/architecture/decisions/ADR-0006-desktop-wails.md).
