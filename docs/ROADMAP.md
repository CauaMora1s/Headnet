# Roadmap

This is the plan for building Headnet, in the order it will be built.

Each phase depends on the ones before it. That ordering is not arbitrary:
NAT traversal is meaningless before peers exist, and peers are meaningless
before devices can enrol. Building out of order produces code that cannot be
tested against anything real.

Every phase below carries the same eleven headings, because
[CONTRIBUTING.md](../CONTRIBUTING.md) asks contributors to think in that order
— architecture, security, API, database, implementation, testing,
observability, documentation, UX, compatibility, deployment.

**There are no dates.** This is a spare-time open-source project and inventing
a schedule would be the same kind of dishonesty the codebase avoids
elsewhere.

---

## Status at a glance

| Phase | | Status |
| --- | --- | --- |
| 0 | [Foundation](#phase-0--foundation) | **Complete** |
| 1 | [Server MVP](#phase-1--server-mvp) | Next |
| 2 | [Client MVP](#phase-2--client-mvp) | Planned |
| 3 | [Device-to-device networking](#phase-3--device-to-device-networking) | Planned |
| 4 | [NAT traversal](#phase-4--nat-traversal) | Planned |
| 5 | [Relay](#phase-5--relay) | Planned |
| 6 | [Authorization](#phase-6--authorization) | Planned |
| 7 | [DNS](#phase-7--dns) | Planned |
| 8 | [Routes](#phase-8--routes) | Planned |
| 9 | [Desktop](#phase-9--desktop) | Planned |
| 10 | [Mobile](#phase-10--mobile) | Planned |
| 11 | [Advanced features](#phase-11--advanced-features) | Planned |
| 12 | [Production hardening](#phase-12--production-hardening) | Planned |

The first genuinely useful milestone is the end of **Phase 3**: two machines
on the same network can reach each other. Phase 4 is what makes that work when
they are not on the same LAN.

---

## Phase 0 — Foundation

**Status: complete.**

### Goal

A repository someone can clone, build, test and run — with the standards,
documentation and safety rails in place before there is any security-sensitive
code to get wrong.

### Dependencies

None.

### What was built

- Monorepo layout separating programs (`apps/`), reusable contracts
  (`packages/`), implementation (`internal/`), deployment and documentation
- Go module, configuration loader, structured logging, database abstraction
  with versioned migrations
- Control-plane server: graceful lifecycle, `/health`, `/live`, `/ready`,
  `/api/v1/version`, `/api/v1/protocol/negotiate`
- Middleware: request IDs, panic recovery, request logging, security headers,
  body limits, per-client rate limiting
- CLI with a working `version` and `diagnostics`
- Svelte web UI reporting real control-plane status
- CI: lint, test on three operating systems, race detector, seven-platform
  build matrix, end-to-end smoke test, container build
- Security workflow: `govulncheck`, CodeQL, dependency review, secret scanning
- Docker, Compose and systemd deployment assets
- Architecture, security, operations and development documentation; eleven ADRs

### Security considerations

The decisions that matter most were made here, because they are the expensive
ones to change later: default-deny as the policy model, private keys never
leaving devices, rate limiting before authentication, strict configuration
validation, and the no-fake-functionality rule.

### Definition of Done

- [x] `make dev` starts a server that answers `/health`
- [x] Format, vet, tidy, lint and the full test suite pass locally
- [x] Every implemented package has tests
- [x] Threat model written
- [x] ADRs for every major decision
- [ ] CI green on Linux, macOS and Windows — the workflows are written but
      have not run yet; there is no remote. This is the one item Phase 0
      cannot tick on its own.

---

## Phase 1 — Server MVP

### Goal

An administrator can create an account, sign in to the web UI, and register a
device record. No VPN yet — this is the identity and inventory layer that
everything else hangs from.

### Dependencies

Phase 0.

### Architecture

Introduces the first real domain model. `internal/auth` gains the provider
abstraction; `internal/devices` and `internal/network` gain repositories over
`internal/storage`.

The authentication abstraction matters more than the local provider it starts
with. Writing per-provider logic for Google, GitHub, Microsoft and Apple is
the wrong shape: they are all OIDC. One `Provider` interface with `local` and
`oidc` implementations covers the whole list.

```
AuthProvider (interface)
├── Local  — email + password, Argon2id
└── OIDC   — any compliant issuer
```

### Security considerations

This is where the project starts holding credentials, and where most of the
classic web vulnerabilities become reachable.

- **Password storage.** Argon2id with per-password salts and parameters chosen
  from current guidance, stored alongside the hash so they can be raised later
  without invalidating existing passwords.
- **Session management.** Server-side sessions in a `Secure`, `HttpOnly`,
  `SameSite=Lax` cookie. Not a JWT: a control plane must be able to revoke a
  session immediately when a device is stolen, and a stateless token cannot be
  revoked.
- **CSRF.** Cookie authentication means state-changing requests need
  protection. Double-submit token, checked on every non-idempotent request.
- **First-run bootstrap.** The window between "server starts" and "an admin
  exists" is a real vulnerability if handled badly. The first account must be
  claimable exactly once, and the server must refuse to serve an unclaimed
  installation to anyone but the first caller.
- **User enumeration.** Login and password-reset responses must not reveal
  whether an account exists — same message, same timing.
- **Rate limiting on authentication**, separately and more tightly than the
  global limiter.
- **Setup keys** (device enrolment credentials): revocable, expiring, scoped,
  optionally single-use, stored hashed so a database read does not yield
  usable keys, and every use audited.

### API changes

```
POST   /api/v1/auth/login
POST   /api/v1/auth/logout
GET    /api/v1/auth/session
POST   /api/v1/auth/bootstrap        first admin, once only
GET    /api/v1/users
POST   /api/v1/users
GET    /api/v1/users/{id}
DELETE /api/v1/users/{id}
GET    /api/v1/devices
POST   /api/v1/devices               register a device record
GET    /api/v1/devices/{id}
DELETE /api/v1/devices/{id}          revoke
POST   /api/v1/setup-keys
GET    /api/v1/setup-keys
DELETE /api/v1/setup-keys/{id}       revoke
```

### Database changes

`users`, `sessions`, `devices`, `setup_keys`, `ip_allocations`.

`devices` stores a **public key only**, and a unique constraint on it. The
`ip_allocations` table exists so address assignment is a database-level
uniqueness guarantee rather than application logic that races.

### Implementation plan

1. ~~IP address management: allocate from the configured pool, never twice,
   reclaim on revocation~~ — done
2. ~~`users` and password hashing~~ — done
3. ~~Sessions and the login flow~~ — done
4. ~~First-run bootstrap~~ — done
5. ~~Setup keys~~ — done
6. ~~Device registration, including setup-key redemption~~ — done
7. ~~Web UI: sign-in, device list, device revocation~~ — done. The
   binary does not yet *serve* the built UI; that moves to Phase 2 with the
   client daemon, and until then it is served by the dev server or a proxy
8. CLI: `admin users`, `admin devices` — deferred to Phase 2, where
   `headnet login` provides the credential storage they need

### Testing

- Argon2id parameters and verification, including rejecting a tampered hash
- Session lifecycle: issue, validate, expire, revoke
- CSRF rejection on every state-changing endpoint
- Bootstrap can only be claimed once, including under concurrency
- IP allocation under concurrent enrolment — no duplicates, no gaps
- Login is constant-shaped for existing and non-existent accounts
- A revoked setup key cannot enrol
- A revoked device loses API access immediately

### Observability

`user.login`, `user.login_failed`, `device.created`, `device.revoked`,
`setup_key.created`, `setup_key.used`, `setup_key.revoked` as structured log
events, with the actor, target and result — the beginnings of the audit log
that Phase 6 formalises.

### Documentation

`docs/authentication.md` fleshed out; a first-run guide; setup-key semantics;
the API specification updated as endpoints land.

### UX

Adding a device is the interaction that has to be excellent. Target: under one
minute, and no cryptographic vocabulary anywhere in the flow.

### Compatibility

Protocol stays at v1; these are additive endpoints.

### Definition of Done

- [ ] An administrator can install, bootstrap, sign in and register a device
- [ ] No endpoint accepts an unauthenticated state change
- [ ] Every new endpoint is in `openapi.yaml`
- [ ] Security review of the authentication and enrolment flows recorded

---

## Phase 2 — Client MVP

### Goal

`headnet up` on a machine enrols it, brings up a WireGuard interface, and
establishes a tunnel to the control plane's own WireGuard endpoint. One
device, one tunnel — but a real one.

### Dependencies

Phase 1.

### Architecture

```
CLI / Desktop UI
      ↓  local IPC (unix socket / named pipe)
Client daemon (headnetd)
      ↓  HTTPS: enrolment, configuration sync
Control plane
      ↓
WireGuard interface
```

The daemon is the only component that touches key material or network
interfaces, because it is the only one that needs privilege. The CLI and the
eventual desktop UI are unprivileged clients of its local API. That boundary
is what keeps a UI bug from becoming a root compromise.

### Security considerations

- **Key generation is local.** The daemon generates the WireGuard private key,
  stores it with 0600 permissions in a root-owned directory, and sends only
  the public key. There is no code path that transmits a private key, and a
  test asserts it.
- **The local API is the new attack surface.** A unix socket with filesystem
  permissions, or a named pipe with an ACL on Windows. Never a TCP port: a TCP
  local API is reachable by every process and, on a shared machine, by every
  user.
- **Privilege separation.** The daemon needs `CAP_NET_ADMIN` (or equivalent),
  not root. Where the platform allows dropping the rest, it does.
- **Instance identity pinning.** The daemon records the control plane's
  instance ID at enrolment. If the ID changes, it is talking to a different
  network at the same address and must refuse to continue rather than silently
  re-enrol.
- **TLS verification is not optional.** No flag to disable it. An operator
  with a private CA installs the CA.

### API changes

```
POST /api/v1/devices/enroll          exchange a setup key for an identity
GET  /api/v1/devices/me
POST /api/v1/devices/me/heartbeat
GET  /api/v1/network/config          address, DNS, peers (empty until Phase 3)
```

### Database changes

`devices` gains `last_seen`, `os`, `hostname`, `client_version`, `endpoints`.

### Implementation plan

1. `internal/wireguard`: interface lifecycle, per platform
2. Local key generation and storage
3. Daemon skeleton: lifecycle, local API, configuration persistence
4. Enrolment against a setup key
5. Configuration sync and heartbeat
6. Interface and address configuration
7. CLI: `up`, `login`, `logout`, `status`, `connect`, `disconnect`
8. `diagnostics` gains real daemon and WireGuard checks

### Testing

- No private key is ever serialised into a request — asserted directly
- Key file permissions on each platform
- Enrolment rejects an expired, revoked or already-used setup key
- Instance-ID change is refused
- Interface creation and teardown, in containers on Linux
- Daemon restart preserves identity; the machine reconnects without
  re-enrolling

### Observability

Daemon logs: enrolment, sync, interface state, reconnection. Connection state
transitions with reasons — a connection that drops must say why.

### Documentation

Client installation per platform; what the daemon stores and where;
`docs/security/key-management.md` updated with real paths.

### UX

The target from the README: install, log in, the device appears, connect. If
this takes more than a minute or requires reading anything about WireGuard,
the design is wrong.

### Compatibility

Protocol still v1. `network.config` is the first endpoint where a version skew
between daemon and server is plausible, so it is where capability negotiation
starts earning its place.

### Definition of Done

- [ ] A fresh machine enrols and brings up an interface with one command
- [ ] `headnet status` reports genuine interface state
- [ ] `headnet diagnostics` no longer reports the daemon as skipped
- [ ] Private keys demonstrably never leave the device
- [ ] Works on Linux, macOS and Windows

---

## Phase 3 — Device-to-device networking

### Goal

Two enrolled devices on the same network can reach each other by VPN address.
**This is the first phase where Headnet is genuinely useful.**

### Dependencies

Phase 2.

### Architecture

The control plane distributes peer configuration; each daemon reconciles its
local WireGuard peer set against it. Reconciliation, not incremental patching:
a daemon that has been offline, or that has lost track, converges to the
correct state from whatever state it is in.

Peer distribution is push-based over a WebSocket, with polling as a fallback,
so that revoking a device takes effect in seconds rather than at the next
poll.

### Security considerations

- **A client receives only the peers it is authorised to reach.** Before
  Phase 6 that is every device on the network; the filtering hook is built
  here so that Phase 6 tightens a rule rather than restructuring the flow.
  Distributing the full peer list to everyone and filtering client-side would
  be a design that cannot be secured later.
- **Revocation must actually disconnect.** Removing a device from a list is
  not revocation; peers must be told to drop it, and the tunnel must die.
- **WebSocket authentication and limits.** Authenticated at handshake, with
  per-connection message rate and size limits, and a cap on concurrent
  connections per device.
- **`AllowedIPs` is the enforcement point.** WireGuard uses it for
  cryptokey routing; a mistake there is a routing vulnerability, not a
  cosmetic bug.

### API changes

```
GET /api/v1/network/peers
WS  /api/v1/network/events           peer changes, revocations, config updates
```

### Database changes

`device_peers` or a derived view; `device_events` for the event stream.

### Implementation plan

1. Peer set computation, with the authorisation hook in place
2. Peer reconciliation in the daemon
3. WebSocket event channel with polling fallback
4. Connection health monitoring and reconnection with backoff
5. Revocation propagation
6. `headnet peers`, `headnet ping`

### Testing

Container-based integration tests: A ↔ B connectivity; peer set changes on
enrolment; revocation severs an established tunnel; daemon restart recovers;
server restart does not disturb established tunnels; network interruption and
recovery.

### Observability

`connected_peers`, `peer_config_updates`, reconnection counts, tunnel
handshake ages.

### Definition of Done

- [ ] Two devices on one LAN reach each other by VPN address
- [ ] Revoking a device drops its tunnels within seconds
- [ ] Integration tests run in CI
- [ ] Peers survive a server restart

---

## Phase 4 — NAT traversal

### Goal

Two devices behind different NATs connect directly, without a relay, in the
common cases.

### Dependencies

Phase 3.

### Architecture

Endpoint discovery via STUN, candidate exchange through the control plane, and
simultaneous-open UDP hole punching. This is well-trodden ground; the design
will follow the published approaches from Tailscale and WebRTC's ICE rather
than being invented here, and an ADR will record which parts are adopted and
which are simplified.

Roughly:

```
Each peer discovers its candidates (local, server-reflexive via STUN)
        ↓
Candidates exchanged through the control plane
        ↓
Simultaneous UDP send from both sides
        ↓
First path that completes a handshake wins; keep probing for better ones
```

### Security considerations

- **Candidate addresses are private information.** They reveal a user's rough
  location and ISP. They go only to peers already authorised to connect.
- **STUN responses are untrusted input.** A malicious or spoofed STUN server
  can lie about a reflexive address; a discovered endpoint is a hint, never a
  fact, and only a completed WireGuard handshake confirms a path.
- **Hole punching must not become an amplification vector.** Rate-limited,
  bounded, and only towards peers that are expecting it.
- **No security property depends on NAT.** NAT is not a firewall. All
  authentication and authorisation is in WireGuard and the policy layer.

### Implementation plan

1. `internal/signaling`: candidate exchange
2. STUN client (RFC 5389), with configurable servers
3. Candidate gathering and prioritisation
4. Coordinated hole punching
5. Path selection, and continuous probing for a better path
6. Real UDP and direct-connectivity checks in `diagnostics`

### Testing

Container networks simulating full-cone, restricted-cone, port-restricted and
symmetric NAT; CGNAT; and the double-symmetric case that must fail over to a
relay. The failure cases matter as much as the successes — the point of this
phase is knowing when direct connection is impossible.

### Definition of Done

- [ ] Direct connections succeed across common NAT combinations
- [ ] Failure to punch is detected quickly and cleanly
- [ ] `headnet diagnostics` reports real UDP and direct-path results
- [ ] The chosen approach is documented in an ADR with its sources

---

## Phase 5 — Relay

### Goal

When a direct path is impossible, traffic still flows — through a relay that
cannot read it.

### Dependencies

Phase 4.

### Architecture

A separate binary (`apps/relay`) that forwards encrypted packets between
authenticated clients. It terminates TLS for its own control channel and
forwards WireGuard packets it holds no key for.

### Security considerations

- **The relay must never be able to decrypt.** This is the phase's defining
  property. It forwards WireGuard ciphertext; it has no WireGuard keys and no
  way to obtain them. A compromised relay observes ciphertext, packet sizes
  and timing — metadata, not content — and that limitation is documented
  plainly rather than glossed over.
- **Authentication.** Clients present a control-plane-issued, short-lived,
  audience-restricted token. An open relay is an open proxy.
- **Abuse resistance.** Per-client rate limits, bandwidth limits, connection
  caps and idle timeouts. A relay is the most exposed component in the system.
- **Failure is not silent.** A client that falls back to a relay says so; a
  user is entitled to know their traffic is taking a longer path through
  another machine.

### Definition of Done

- [ ] Two devices that cannot connect directly communicate through a relay
- [ ] The relay demonstrably cannot decrypt, shown by a test
- [ ] Fallback is automatic, and visible to the user
- [ ] Relay health and metrics exposed

---

## Phase 6 — Authorization

### Goal

Default-deny access policy: users, groups, device tags, and rules governing
which devices may reach which.

### Dependencies

Phase 3 (the peer-filtering hook), Phase 1 (users).

### Architecture

A policy engine evaluating rules over subjects (users, groups, tags) and
destinations (groups, tags, CIDRs), producing the per-device peer sets that
Phase 3 distributes. Enforcement is in the peer set and in `AllowedIPs` — a
device that is not permitted to reach another is never given the configuration
to try.

```yaml
rules:
  - source: { group: developers }
    destination: { group: servers }
    protocol: tcp
    ports: [22]
    action: allow
```

### Security considerations

- **Default deny, with no way to accidentally invert it.** An empty policy
  means no traffic, not all traffic.
- **Policy changes propagate promptly**, or a revoked permission is not a
  revoked permission.
- **A policy simulator before saving.** The most dangerous administrative
  action here is one that silently opens more than intended; an administrator
  must be able to see the effect before committing it.
- **Audit everything.** Who changed which rule, when, and what it did.

### Definition of Done

- [ ] Default deny enforced, with tests for the empty-policy case
- [ ] Policy changes take effect within seconds
- [ ] Audit log records every policy and membership change
- [ ] The security model is documented for administrators, not just developers

---

## Phase 7 — DNS

### Goal

`ssh nas` instead of `ssh 100.100.0.7`.

### Dependencies

Phase 3.

### Architecture

Names derived from device hostnames within a network-scoped domain. An
established DNS server will be evaluated before writing one — building a DNS
server is a large project with a bad failure mode, and the decision will be
recorded in an ADR either way.

### Security considerations

- Name assignment must not be spoofable by a device choosing its own hostname
- No leaking internal names to public resolvers
- Split DNS must not break the user's existing resolution
- DNS is a classic exfiltration channel; queries are rate-limited

### Definition of Done

- [ ] Devices are reachable by name on every supported platform
- [ ] Existing DNS keeps working
- [ ] Names cannot be hijacked by a device claiming another's hostname

---

## Phase 8 — Routes

### Goal

Reach a whole subnet — a home LAN, an office network, a cloud VPC — through a
device on it.

### Dependencies

Phases 3 and 6.

### Security considerations

- **Advertisement is not approval.** A device may advertise a route; an
  administrator must approve it. Without that, one compromised device can
  claim `0.0.0.0/0` and become a man in the middle for the whole network.
- Overlapping and conflicting routes are detected and refused
- Route usage is subject to policy, like everything else

### Definition of Done

- [ ] A subnet router makes its LAN reachable to authorised devices
- [ ] Unapproved routes are never distributed
- [ ] Route removal takes effect promptly

---

## Phase 9 — Desktop

### Goal

A tray application for Windows, macOS and Linux.

### Dependencies

Phase 3.

### Architecture

Wails (Go + Svelte), talking to the local daemon over its IPC API. The UI
never implements networking logic and is never the source of truth for
network state — it displays what the daemon reports. See
[ADR-0006](architecture/decisions/ADR-0006-desktop-wails.md).

### Definition of Done

- [ ] Installable on all three platforms
- [ ] Tray icon reflects real connection state
- [ ] Sign-in, status and diagnostics available without a terminal

---

## Phase 10 — Mobile

### Goal

Android and iOS clients.

### Dependencies

Phase 4 (mobile networks make NAT traversal essential).

### Architecture

Flutter for the UI, with platform-native VPN integration underneath —
`VpnService` on Android, `NetworkExtension` on iOS. The desktop daemon does
not run on mobile and no attempt will be made to make it; what is reused is
the API contract, the authentication protocol, the data model and the
configuration format. See
[ADR-0007](architecture/decisions/ADR-0007-mobile-flutter.md).

### Security considerations

- Keys in the platform keystore or Secure Enclave, never in app storage
- QR enrolment codes are short-lived, single-use, and scoped
- Background restrictions must not silently drop the tunnel — a VPN that
  quietly stops is a security failure, not a bug

### Definition of Done

- [ ] Both platforms connect and stay connected
- [ ] QR enrolment works
- [ ] Keys are in platform-protected storage

---

## Phase 11 — Advanced features

Exit nodes, port forwarding, device sharing, multiple networks, teams, API
tokens, webhooks, SSO and MFA.

**Exit nodes** are called out specifically: routing all of a user's internet
traffic through another machine is a fundamentally different trust decision
from reaching a private service, and it gets explicit authorisation, an
unmistakable UI, and its own threat-model section. It is not a variation on
subnet routing.

---

## Phase 12 — Production hardening

High availability, PostgreSQL tuning, multiple relays, backup and restore,
disaster recovery, trusted-proxy support (so rate limiting works per client
behind a load balancer), Prometheus metrics, an external security audit, and
performance and load testing.

The [known limitations](security/threat-model.md#7-known-limitations) accepted
in earlier phases are resolved here or explicitly re-accepted.

---

## How to propose a change to this roadmap

Open a [discussion](https://github.com/CauaMora1s/Headnet/discussions). Arguments
that a phase is in the wrong order, or that something is missing from a
phase's security considerations, are especially welcome — those are the
mistakes that are expensive to discover later.
