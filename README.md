# Headnet

A self-hosted mesh VPN control plane, built on WireGuard.

Headnet aims to make a private network between your machines as easy to run as
a container, without giving up control of it. You host the whole thing. Your
devices connect directly to each other. Nobody else holds your keys.

> **Project status: early foundation. There is no working VPN yet.**
>
> This repository currently contains the project skeleton described in
> [Phase 0 of the roadmap](docs/ROADMAP.md): a control-plane server that
> starts, stores state and reports its health; a CLI; a web UI; and the
> testing, documentation and deployment scaffolding around them.
>
> No devices can be enrolled, no keys are managed, and **no traffic is
> carried**. Nothing in this build simulates otherwise — every unimplemented
> feature says so, in the API, in the CLI and in the UI. See
> [What actually works today](#what-actually-works-today).

---

## Why

WireGuard is excellent and almost nobody configures it by hand, because doing
so means understanding key exchange, NAT traversal, routing tables and
firewall rules — for every device, every time one is added.

Tools like Tailscale solved that, brilliantly, by putting a coordination
service in front of WireGuard. But the good ones are hosted, and the
self-hostable ones tend to assume you are comfortable with the networking
underneath.

Headnet is an attempt at both: the ease of a managed mesh VPN, with the
control plane running on your own VPS, home server, NAS or Raspberry Pi.

The experience it is built towards:

```
Deploy the server  →  Open the web UI  →  Create an account
                                                  ↓
Private network works  ←  Connect  ←  Install the client  →  Log in
```

You should never need to know what a public key is to use it.

## Design principles

These are load-bearing. Where a decision in this repository looks unusual, it
is usually one of these being applied.

- **Security first.** Private keys are generated on the device and never leave
  it. The server stores public keys only. Access policy is default-deny.
- **Simplicity first.** One binary, one database file. No cluster required,
  ever.
- **Self-hosted first.** No cloud service, no phone-home, no account with us.
- **WireGuard for the data plane.** We do not invent cryptography. The control
  plane manages identity, devices and policy; WireGuard carries the packets.
- **Direct connections preferred.** Relays exist only for when a direct path
  is impossible, and a relay can never decrypt what passes through it.
- **Honesty over polish.** An unimplemented feature must be visibly
  unimplemented. See [No fake functionality](#no-fake-functionality).

## What actually works today

Everything in this list has tests behind it and can be verified by running the
commands under [Quick start](#quick-start).

| Working now | |
| --- | --- |
| Control-plane server | Starts, binds, shuts down gracefully |
| Configuration | YAML file + environment variables, validated at start-up |
| Database | SQLite and PostgreSQL, with versioned migrations |
| Health endpoints | `/health`, `/live`, `/ready` with real dependency probes |
| Build and protocol API | `/api/v1/version`, `/api/v1/protocol/negotiate` |
| Structured logging | JSON or text, request IDs, no secrets |
| Rate limiting | Per-client token bucket, on by default |
| CLI | `version`, `diagnostics` |
| Web UI | First-run setup, sign-in, dashboard, device management |
| Accounts | First-run setup, sign-in, sessions, CSRF |
| Devices | Register, enrol with a setup key, list, revoke |
| Setup keys | Create, list, revoke — expiring and optionally single-use |
| Addressing | Automatic IPv4/IPv6 allocation and reclamation |
| Device keys | Curve25519 generation and storage on the device, private to it |

| Not built yet | Planned for |
| --- | --- |
| Serving the web UI from the server binary | [Phase 2](docs/ROADMAP.md#phase-2--client-mvp) |
| Client daemon and WireGuard interfaces | [Phase 2](docs/ROADMAP.md#phase-2--client-mvp) |
| `headnet login` and the admin CLI | [Phase 2](docs/ROADMAP.md#phase-2--client-mvp) |
| Peer distribution, device-to-device traffic | [Phase 3](docs/ROADMAP.md#phase-3--device-to-device-networking) |
| NAT traversal | [Phase 4](docs/ROADMAP.md#phase-4--nat-traversal) |
| Relay | [Phase 5](docs/ROADMAP.md#phase-5--relay) |
| Access policies, groups, audit log | [Phase 6](docs/ROADMAP.md#phase-6--authorization) |
| DNS | [Phase 7](docs/ROADMAP.md#phase-7--dns) |
| Subnet routes and exit nodes | [Phase 8](docs/ROADMAP.md#phase-8--routes) |
| Desktop and mobile apps | [Phases 9–10](docs/ROADMAP.md#phase-9--desktop) |
| Metrics, HA, backups | [Phase 12](docs/ROADMAP.md#phase-12--production-hardening) |

## Quick start

You will need [Go 1.25+](https://go.dev/dl/) and, for the web UI,
[Node 20+](https://nodejs.org/) with [pnpm](https://pnpm.io/).

```bash
git clone https://github.com/CauaMora1s/Headnet.git
cd headnet
make dev
```

On Windows, where `make` is not available:

```powershell
.\scripts\dev.ps1 dev
```

The server prints its listening address and creates `./data/headnet.db` on
first run. Then:

```bash
curl http://localhost:8080/health
```

```json
{
  "status": "ok",
  "version": "0.0.0-dev",
  "uptime_seconds": 3,
  "checks": [{ "name": "database", "status": "ok", "duration_ms": 0 }]
}
```

For the web UI, in a second terminal:

```bash
make dev-web
```

It serves on <http://localhost:5173> and proxies the API to the Go server.

To check what a build can and cannot do:

```bash
go run ./apps/cli diagnostics -server http://localhost:8080
```

```
  ✓  CLI build                 0.0.0-dev (unknown, linux/amd64)
  –  DNS                       the server was given as a literal IP address, so no lookup was needed
  ✓  Control server            localhost:8080 is reachable and reports "ok" (version 0.0.0-dev)
  ✓  Protocol compatibility    agreed on protocol v1 (this build speaks 1–1, the server speaks 1–1)
  –  Client daemon             not built yet (Phase 2)
  –  WireGuard interface       not built yet (Phase 2)
  –  Direct peer connectivity  not built yet (Phase 4)
  –  Relay fallback            not built yet (Phase 5)
```

More detail in [docs/getting-started.md](docs/getting-started.md).

## Architecture

The control plane never carries VPN traffic. It hands out identity, addresses
and policy; WireGuard does the rest, directly between peers.

```
                   ┌──────────────────────┐
                   │    Control plane     │   identity, devices,
                   │  (headnet-server)    │   policy, addressing
                   └──────────┬───────────┘
                              │ HTTPS: enrolment, config sync
              ┌───────────────┴───────────────┐
              │                               │
        ┌─────┴──────┐                  ┌─────┴──────┐
        │  Device A  │                  │  Device B  │
        │  (daemon)  │                  │  (daemon)  │
        └─────┬──────┘                  └─────┬──────┘
              │                               │
              │   WireGuard, encrypted end to end
              ├───────────── direct ──────────┤
              │                               │
              └──────► Relay (fallback) ──────┘
                       cannot decrypt
```

Private keys are generated on each device and stay there. The server holds
public keys. A relay forwards ciphertext it has no way to read.

Details in [docs/architecture.md](docs/architecture.md), and the reasoning
behind each major choice in
[docs/architecture/decisions/](docs/architecture/decisions/).

## Repository layout

```
apps/          Programs: server, cli, client, relay, web, desktop, mobile
packages/      Shared, reusable contracts (Apache-2.0) — protocol, api, config
internal/      Control-plane implementation (AGPL-3.0)
deploy/        Docker, Compose, systemd, Kubernetes
docs/          Architecture, security, operations, roadmap
scripts/       Development helpers
test/          Integration and end-to-end suites
```

`apps/client`, `apps/relay`, `apps/desktop`, `apps/android` and `apps/ios` are
placeholders for later phases and contain no implementation.

## No fake functionality

This is a rule the project takes seriously enough to test for.

A VPN client that reports "connected" when nothing is running does not merely
mislead someone — it can convince them their traffic is protected while it is
in the clear. So, in this codebase:

- Unimplemented API endpoints return `501` naming the roadmap phase, or `404`
  because they genuinely do not exist. They never return an empty success.
- Unimplemented CLI commands exit non-zero and say what is missing. There is a
  test asserting that no command ever prints "connected".
- `headnet diagnostics` reports unbuilt subsystems as *skipped*, never as
  passing.
- The web UI lists what is unbuilt rather than showing empty tables.
- `/metrics` returns `501` rather than an empty page a dashboard would render
  as a healthy zero.

## Documentation

| | |
| --- | --- |
| [Getting started](docs/getting-started.md) | Install and run it |
| [Architecture](docs/architecture.md) | How the pieces fit together |
| [Configuration](docs/configuration.md) | Every setting, and what it does |
| [Deployment](docs/deployment.md) | Docker, Compose, systemd, reverse proxies |
| [Networking](docs/networking.md) | Addressing, NAT traversal, relays |
| [Authentication](docs/authentication.md) | The planned identity model |
| [Troubleshooting](docs/troubleshooting.md) | When something is wrong |
| [Upgrading](docs/upgrade.md) | Versions, migrations, rollback |
| [Roadmap](docs/ROADMAP.md) | What is planned, in what order |
| **Security** | |
| [Threat model](docs/security/threat-model.md) | What is trusted, and what is not |
| [Security architecture](docs/security/security-architecture.md) | The controls, and where they sit |
| [Key management](docs/security/key-management.md) | Where key material lives |
| **Development** | |
| [Setup](docs/development/setup.md) | Getting a working environment |
| [Testing](docs/development/testing.md) | How this project is tested |
| [Internal architecture](docs/development/architecture.md) | Package layout and boundaries |
| [Definition of Done](docs/development/definition-of-done.md) | The bar for merging |
| [API](docs/api/) | OpenAPI specification |

## Contributing

Contributions are welcome, especially at this stage while the foundations are
still soft. Start with [CONTRIBUTING.md](CONTRIBUTING.md), and please read the
[threat model](docs/security/threat-model.md) before touching anything that
handles identity, keys or policy.

Found a vulnerability? Do not open an issue —
[SECURITY.md](SECURITY.md) explains how to report it privately.

## Licence

Headnet is licensed under the **GNU Affero General Public License v3.0 or
later** ([LICENSE](LICENSE)), with one deliberate exception: everything under
[`packages/`](packages/) — the protocol definitions, API types and
configuration loader that a third party needs in order to interoperate — is
**Apache-2.0** ([packages/LICENSE](packages/LICENSE)).

The reasoning, and the alternatives considered, are in
[docs/licensing.md](docs/licensing.md) and
[ADR-0009](docs/architecture/decisions/ADR-0009-licensing.md). If you are
evaluating Headnet for commercial use and this split is a problem, please open
a discussion — it is early enough to change.

## Prior art

Headnet borrows architecture, and learns from, several projects worth your
attention: [WireGuard](https://www.wireguard.com/),
[Tailscale](https://tailscale.com/), [Headscale](https://github.com/juanfont/headscale),
[NetBird](https://netbird.io/), [Nebula](https://github.com/slackhq/nebula) and
[Netmaker](https://www.netmaker.io/). Where this project follows one of their
designs, the ADR says so.
