# Client daemon (headnetd)

**Status: not implemented.** Planned for
[Phase 2](../../docs/ROADMAP.md#phase-2--client-mvp).

This directory is a placeholder. It contains no code, deliberately — an empty
directory that looks like a component is worse than an honest note.

## What will go here

The background service that runs on each member device. It is the only
component that touches key material or network interfaces, because it is the
only one that needs privilege.

It will be responsible for:

- Generating and storing this device's WireGuard private key, which never
  leaves the machine
- Enrolling with a control plane, interactively or with a setup key
- Synchronising configuration and maintaining the WireGuard interface
- Peer discovery, NAT traversal and relay fallback
- Routing and DNS configuration
- Connection health, reconnection and diagnostics
- A local API over a unix socket (or a named pipe on Windows) for the CLI and
  the desktop UI

The local API is deliberately not a TCP port: a TCP local API is reachable by
every process on the machine and, on a shared host, by every user.
