# Architecture

How Headnet is put together, and why.

For the reasoning behind individual decisions, see the
[ADRs](architecture/decisions/). For the security analysis, see the
[threat model](security/threat-model.md).

---

## The central idea

Headnet separates two things that are usually tangled together:

- **The control plane** decides *who may talk to whom*. It manages identity,
  devices, addresses and policy.
- **The data plane** carries the packets. That is WireGuard, running directly
  between devices.

The control plane is never on the data path. It hands each device the
configuration it needs and then gets out of the way. A device with a
configured tunnel keeps working if the control plane goes down entirely — what
stops is enrolment, revocation and policy changes, not existing connections.

This split is what makes the security model tractable. The server holds public
keys and policy; it holds nothing that could decrypt traffic. Compromising it
is bad — an attacker learns the shape of your network and can add devices —
but it does not yield a single plaintext packet.

```
                    ┌────────────────────┐
                    │   Control plane    │
                    │  (headnet-server)  │
                    │                    │
                    │  identity          │
                    │  devices           │
                    │  addresses         │
                    │  policy            │
                    └─────────┬──────────┘
                              │  HTTPS
                              │  enrolment, config sync, events
                ┌─────────────┴─────────────┐
                │                           │
         ┌──────┴───────┐           ┌───────┴──────┐
         │   Device A   │           │   Device B   │
         │              │           │              │
         │  headnetd    │           │  headnetd    │
         │  private key │           │  private key │
         │  wg0         │           │  wg0         │
         └──────┬───────┘           └───────┬──────┘
                │                           │
                │   WireGuard, encrypted end to end
                ├────────── direct ─────────┤   (preferred)
                │                           │
                └──────► Relay ◄────────────┘   (fallback only)
                     forwards ciphertext
```

---

## Components

### Control plane — `apps/server`

**Status: foundation built.** Serves health and version endpoints today;
identity and devices arrive in [Phase 1](ROADMAP.md#phase-1--server-mvp).

A single Go binary with an embedded database, holding:

- Users and their authentication
- Device records: public key, assigned addresses, metadata, last seen
- Address allocation from the configured pools
- Access policy
- The audit log

It exposes a versioned REST API and, from Phase 3, a WebSocket channel for
pushing configuration changes so that revoking a device takes effect in
seconds rather than at the next poll.

### Client daemon — `apps/client`

**Status: not built.** [Phase 2](ROADMAP.md#phase-2--client-mvp).

A background service on each device, responsible for everything privileged:
key generation and storage, the WireGuard interface, routing, DNS, NAT
traversal, relay fallback, and reconnection.

It exposes a local API over a unix socket (or a named pipe on Windows) that
the CLI and desktop UI consume. That boundary is deliberate: the daemon is the
only component that needs privilege, so it is the only one that has any.

### CLI — `apps/cli`

**Status: `version` and `diagnostics` work.** The rest is declared but
unimplemented, and says so.

`headnet` is a first-class interface, not an afterthought — a headless server
being provisioned by a script needs it more than the UI.

### Web UI — `apps/web`

**Status: reports real control-plane status.** Administrative screens arrive
with the features behind them.

Svelte, TypeScript, Vite and Tailwind, built to a static bundle the Go binary
will eventually embed — so deploying Headnet stays "run one binary" and never
requires a Node runtime in production.

### Relay — `apps/relay`

**Status: not built.** [Phase 5](ROADMAP.md#phase-5--relay).

A separate binary that forwards encrypted packets between clients that cannot
reach each other directly. It holds no WireGuard keys and cannot decrypt what
passes through it.

### Desktop and mobile

**Status: not built.** Phases 9 and 10. Wails for desktop, Flutter for mobile,
both talking to platform-appropriate networking underneath. See
[ADR-0006](architecture/decisions/ADR-0006-desktop-wails.md) and
[ADR-0007](architecture/decisions/ADR-0007-mobile-flutter.md).

---

## Repository layout

A monorepo, because the protocol is shared and a change to it must land
atomically across the server and every client. See
[ADR-0003](architecture/decisions/ADR-0003-monorepo.md).

```
apps/            Programs. Each directory is a binary or an application.
  server/        Control plane                          built
  cli/           Command-line interface                 built
  client/        Client daemon                          Phase 2
  relay/         Relay server                           Phase 5
  web/           Administrative web UI                  built
  desktop/       Wails desktop app                      Phase 9
  android/ ios/  Flutter mobile apps                    Phase 10

packages/        Shared contracts. Apache-2.0, so third parties can
                 interoperate without taking on the AGPL.
  protocol/      Version and capability negotiation
  api/           HTTP DTOs and the error contract
  config/        Layered configuration loading, secret redaction
  shared/        Small dependency-free helpers

internal/        Control-plane implementation. AGPL-3.0. Not importable
                 outside this module, enforced by the Go toolchain.
  auth/          Authentication providers                Phase 1
  devices/       Device lifecycle                        Phase 1
  network/       Addressing and IPAM                     Phase 1
  wireguard/     Interface management                    Phase 2
  signaling/     NAT-traversal coordination              Phase 4
  relay/         Relay coordination                      Phase 5
  acl/           Policy engine                           Phase 6
  dns/           Internal DNS                            Phase 7
  routes/        Subnet routing                          Phase 8
  storage/       Database and migrations                 built
  config/        Server configuration schema             built
  logging/       Structured logging                      built
  httpapi/       Middleware and response helpers         built
  server/        Routing and lifecycle                   built
  cliutil/       Command tree                            built
  version/       Build identity                          built

deploy/          Docker, Compose, systemd, Kubernetes
docs/            This documentation
scripts/         Development helpers
test/            Integration and end-to-end suites
```

Directories for unbuilt components contain a placeholder explaining what will
go there and when. They contain no code.

### Why `packages/` and `internal/` are separate

`internal/` is a Go convention with teeth: the toolchain refuses to let any
module outside this one import it. That makes it the right home for
implementation we want to be free to change.

`packages/` is the opposite — the surface a third party writing an alternative
client legitimately needs. It is licensed Apache-2.0 for exactly that reason
(see [licensing](licensing.md)), which imposes a rule worth stating: **nothing
in `packages/` may import from `internal/`**, and nothing in `packages/` may
take a copyleft dependency.

---

## Request flow

What happens today for `GET /health`:

```
                    Recoverer          a panic becomes a 500, not a dead server
                        ↓
                    RequestID          generated here; inbound headers ignored
                        ↓
                    WithLogger
                        ↓
                    RequestLogger      path only, never the query string
                        ↓
                    SecurityHeaders    HSTS only when base_url is HTTPS
                        ↓
                    RateLimit          before routing, before auth
                        ↓
                    MaxBodyBytes
                        ↓
                    ServeMux           Go 1.22+ method-and-pattern routing
                        ↓
                    handler
```

The order is the security-relevant part:

- **Recoverer is outermost**, so it catches a panic raised anywhere inside,
  including in another middleware.
- **RequestID precedes the logger**, so every line — including one written by
  the recoverer — carries an ID.
- **RateLimit precedes routing and authentication.** Login, enrolment and
  protocol negotiation are necessarily unauthenticated; a limiter that only
  ran after authentication would protect nothing that matters.

---

## Data model

The shape the control plane is being built towards:

```
User
 ├── credentials (local password hash, or an OIDC subject)
 └── Devices
      ├── ID
      ├── Public key          the server never sees the private half
      ├── VPN addresses       allocated from the configured pools
      ├── OS, hostname
      ├── Last seen
      ├── Status              active, revoked
      └── Tags                what policy is written against
```

Two properties this model is designed to guarantee:

- A device row holds a public key and nothing that could decrypt traffic.
- Address allocation is a database uniqueness constraint, not application
  logic that races under concurrent enrolment.

---

## Storage

SQLite by default; PostgreSQL for larger deployments. Both through
`database/sql`, so the repositories written in later phases stay
backend-agnostic. Dialect differences — placeholder syntax, timestamp types —
are confined to `internal/storage`.

The SQLite driver is pure Go, which is what lets `CGO_ENABLED=0` produce
static binaries for seven platforms from one CI job. See
[ADR-0005](architecture/decisions/ADR-0005-database-sqlite-postgres.md).

Migrations are embedded, versioned and checksummed. Editing an applied
migration is refused rather than reconciled, because that is how two
deployments of the same version quietly end up with different schemas. A
database migrated by a newer build is also refused, because running an old
binary against a schema it has never seen corrupts data.

---

## Protocol versioning

Servers and clients are upgraded independently — a fleet of laptops does not
restart when an administrator upgrades the control plane, and a subnet router
may lag by months.

- One monotonically increasing integer. No minor or patch protocol versions.
- Each side advertises the newest version it speaks and the oldest it accepts;
  they are compatible when the ranges overlap.
- Optional behaviour is gated on **named capabilities**, never on a version
  comparison. Adding a capability is always backwards compatible.
- Unknown capabilities are ignored, so an old peer talking to a new one
  degrades rather than fails.

`POST /api/v1/protocol/negotiate` performs this exchange, turning a mismatch
into one clear error at the start of a session instead of a confusing failure
several requests later. See
[protocol-versioning.md](architecture/protocol-versioning.md).

---

## What is deliberately not here

- **No message queue.** A control plane for a few hundred devices does not
  need one, and it would be another thing to operate.
- **No microservices.** One binary. The relay is separate because it has a
  genuinely different trust and deployment profile, not for architectural
  fashion.
- **No Kubernetes requirement.** Headnet must run on a Raspberry Pi.
- **No cloud dependency.** No phone-home, no account with us, no hosted
  component required for anything.
- **No custom cryptography.** WireGuard, TLS, Argon2id. Nothing home-grown.
