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

**Devices**

- Device registration and revocation. A device record holds a WireGuard
  **public** key and an address, and there is no field anywhere — in the
  database or the API — for a private one. Public keys are validated as base64
  decoding to 32 bytes, and an all-zero key is refused because that is what an
  uninitialised buffer looks like.
- Setup keys: revocable, expiring after seven days by default, optionally
  single-use, and returned exactly once. Only a SHA-256 hash is stored, plus a
  short display hint that identifies a key in a list without being able to
  spend it. Their scope (tags) is stored but not yet enforced; Phase 6 will.
- Headless enrolment: `POST /api/v1/devices/enroll` redeems a setup key and
  registers the device it authorises, without a session — the key is the
  credential. Every rejected key looks identical to the caller, so a rejection
  cannot confirm that a key once existed.
- Single-use means single-use under concurrency: the use count is checked in
  the `UPDATE`'s `WHERE` clause rather than read and then compared, so two
  simultaneous enrolments cannot both spend the last use. Verified against
  PostgreSQL, where the race is real.
- Revocation releases the device's addresses back to the pool in the same
  transaction, and a revoked public key stays blocked permanently — one that
  could be registered again would not really be revoked.
- A member sees and manages only their own devices; an administrator sees all.
  The scope is applied in the store query rather than in a handler, so a future
  endpoint cannot forget it. Another user's device is reported as `404` rather
  than `403`, because "you may not see this" confirms it exists.

**Identity**

- Sign-in, sign-out and server-side sessions, in a `Secure` `HttpOnly`
  `SameSite=Lax` cookie. `Secure` follows the base URL scheme, so a
  plain-HTTP development server still works. Neither the session token nor the
  CSRF token is stored in recoverable form, so a stolen database yields
  nothing replayable. Logging out revokes immediately rather than waiting for
  an expiry.
- CSRF protection on every state-changing request. The token is bound to the
  session server-side rather than merely matched against a cookie, so an
  attacker who can set a cookie on the victim's domain still cannot produce a
  valid pair.
- First-run bootstrap: the first account is created through the API and is
  necessarily an administrator. The claim is exactly-once and atomic, so
  concurrent requests cannot both succeed, and a rejected account does not
  consume the claim. **The endpoint is open until claimed** — a deliberate
  trade-off for the deploy-then-create-an-account flow, mitigated by a warning
  logged on every start until it is closed, an audited claim, and the tighter
  authentication rate limit. See the threat model.
- A failed sign-in cannot reveal whether an account exists: an unregistered
  address is verified against a dummy hash so the work, the message and the
  code all match a wrong password.
- A separate, tighter rate limit for `/api/v1/auth/*`, because those endpoints
  are unauthenticated by necessity and repeated guessing is the whole attack.
- User accounts, with Argon2id password hashing at current OWASP parameters.
  The parameters are stored alongside each hash in PHC string format, so they
  can be raised later without invalidating existing passwords; a login against
  a weaker hash reports that it should be upgraded.
- Email addresses are stored both as typed and lower-cased, with the unique
  index on the normalised form, so one address cannot become two accounts by
  differing in case. Plus-suffixes and dots are preserved, because folding them
  is a provider-specific convention that would silently merge two people.
- Password hashes cannot leave the `User` type: unexported to keep them from a
  JSON encoder, with `String`/`GoString` to keep them out of `%+v` in a log
  call or an error message.

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
- First-run setup, sign-in and sign-out, with authentication state derived once
  and shared, so a page cannot render a signed-in shell around a session the
  server has already discarded
- A devices page: register, list and revoke. Revoking asks for confirmation and
  names the device and its address, because a mis-clicked revoke removes a
  machine from a network that may be the only way to reach it
- A hand-written router over the History API. Four routes did not justify a
  dependency, for the same reason the Go side uses the standard library's mux
- CSRF tokens attached to state-changing requests and not to reads
- Live control-plane status from real endpoints
- An explicit list of what has not been built, rather than empty tables. Areas
  whose API works but whose screens do not are marked "API only" — claiming
  something is missing when it works is the same failure as the reverse
- **Not yet served by the server binary.** The UI runs against the dev server or
  behind the same reverse proxy as the API; embedding it is a Phase 2 item

**Infrastructure**

- CI: lint, tests on Linux, macOS and Windows, a PostgreSQL job against a real
  PostgreSQL 17 service, race detector, seven-platform build matrix,
  end-to-end smoke test, container build
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
