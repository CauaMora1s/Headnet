# Changelog

Notable changes to Headnet.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project follows [Semantic Versioning](https://semver.org/) from its
first release.

Security-relevant changes are marked **Security** and, once there are tagged
releases, are accompanied by an advisory. See [SECURITY.md](SECURITY.md).

---

## [Unreleased]

The project foundation: [Phase 0](docs/ROADMAP.md#phase-0--foundation).

No release has been tagged yet. Everything below is on `main`.

> **This build contains no VPN functionality.** The control plane starts,
> stores state and reports its health. No devices can be enrolled, no keys are
> managed, and no traffic is carried. Every unimplemented feature says so.

### Added

**Networking**

- IP address management: addresses are allocated from the configured IPv4 and
  IPv6 pools, one per enabled family, and released when their owner is removed.
  Uniqueness is guaranteed by a database primary key rather than by application
  logic, so concurrent enrolment cannot issue the same address twice. Network
  and broadcast addresses are never handed out, and allocation runs inside the
  caller's transaction so a failed enrolment leaks nothing.

**Control plane**

- HTTP server with graceful shutdown and bounded timeouts, including a
  separate header-read timeout against slowloris clients
- `/health`, `/live` and `/ready` with real dependency probes. `/live` checks
  nothing but the process, so a brief database outage cannot trigger a restart
  loop
- `GET /api/v1/version` — build identity and supported protocol range
- `POST /api/v1/protocol/negotiate` — version and capability negotiation
- `/metrics` returning `501` when enabled, `404` when not — never an empty
  page a dashboard would read as a healthy zero

**Configuration**

- Layered loading: defaults, YAML file, environment variables
- Exhaustive validation at start-up, reporting every problem at once rather
  than one restart per typo
- Unknown YAML keys rejected, so a typo in a security-relevant setting cannot
  silently leave a permissive default in place
- Address-pool validation: reserved ranges, host bits, prefix sizes, family
  mismatches, IPv4/IPv6 overlap
- `-print-config` showing the effective configuration with secrets masked

**Storage**

- SQLite (default) and PostgreSQL through `database/sql`
- Pure-Go SQLite driver, so `CGO_ENABLED=0` builds and cross-compilation work
- Embedded, versioned, checksummed migrations for both dialects
- Migration ledger that refuses an edited migration and refuses a database
  migrated by a newer build
- Persistent instance identity, so a client can later detect that it is
  talking to a different network at the same address

**CLI**

- `headnet version`
- `headnet diagnostics` — real checks against a control server, with a remedy
  for every failure, and unbuilt subsystems reported as *skipped* rather than
  passing
- Documented exit codes, including `3` for "not implemented in this build" so
  scripts can distinguish it from a failure

**Web UI**

- Svelte 5, TypeScript, Vite and Tailwind
- Live control-plane status from real endpoints
- An explicit list of what has not been built, rather than empty tables

**Infrastructure**

- CI: lint, tests on Linux, macOS and Windows, race detector, seven-platform
  build matrix, end-to-end smoke test, container build
- Security workflow: `govulncheck`, CodeQL, dependency review, secret scanning
  over full history, frontend audit
- Distroless container image, non-root, read-only root filesystem
- Docker Compose and a hardened systemd unit
- Makefile and a PowerShell equivalent for Windows

**Documentation**

- Architecture, threat model, security architecture, key management
- Roadmap through Phase 12, each phase with its dependencies, security
  considerations and Definition of Done
- Eleven ADRs
- Getting started, configuration, deployment, networking, authentication,
  troubleshooting, upgrading
- OpenAPI 3.1 specification covering exactly what is implemented

### Security

- Per-client rate limiting, **on by default**, applied before routing and
  before authentication. Buckets are swept so the limiter cannot itself become
  a memory-exhaustion vector
- Forwarding headers deliberately **not** trusted for client identification,
  so rotating one cannot reset a rate-limit bucket. The consequence — one
  bucket per proxy — is documented in
  [deployment](docs/deployment.md#rate-limiting-behind-a-proxy--important)
- Inbound `X-Request-Id` ignored rather than adopted, closing a log-injection
  and log-forgery vector
- Query strings excluded from request logging, because OIDC callbacks and
  enrolment flows carry codes in them
- Plain-HTTP `base_url` refused in production unless explicitly opted out
- HSTS sent only for an HTTPS deployment, so a development server cannot pin a
  browser to HTTPS on localhost
- Security headers on every response, including a strict
  `Content-Security-Policy`
- Secret redaction wherever configuration is rendered, preserving the
  distinction between a set and an unset secret
- Panic recovery that discloses nothing to the client
- Request bodies capped; JSON decoding rejects unknown fields
- Error responses free of internal paths, driver detail and connection strings

### Known limitations

- **No VPN functionality.** Phases 1 to 3.
- Rate limiting keys on the transport peer address; behind a proxy, all
  clients share one bucket (Phase 12)
- SQLite is held to a single connection, so reads serialise behind writes
  (Phase 12)
- No metrics, no HA, no backup tooling (Phase 12)
- **No security audit.** Do not use this to protect anything that matters.

### Licence

AGPL-3.0-or-later, with `packages/` under Apache-2.0 so third parties can
interoperate. See [licensing](docs/licensing.md) and
[ADR-0009](docs/architecture/decisions/ADR-0009-licensing.md).

[Unreleased]: https://github.com/CauaMora1s/Headnet/commits/main
