# Security architecture

Where each security control sits, and why it sits there.

The [threat model](threat-model.md) says what we are defending against. This
document says how, and — where a control does not exist yet — marks it
plainly.

---

## Layers

```
┌─────────────────────────────────────────────────────────┐
│  Transport            TLS to the control plane          │
│                       WireGuard between devices         │
├─────────────────────────────────────────────────────────┤
│  Identity             Users, devices, setup keys        │
├─────────────────────────────────────────────────────────┤
│  Authorization        Default-deny policy, peer sets    │
├─────────────────────────────────────────────────────────┤
│  Application          Rate limits, input validation,    │
│                       security headers, audit logging   │
├─────────────────────────────────────────────────────────┤
│  Platform             Non-root, read-only rootfs,       │
│                       dropped capabilities, sandboxing  │
└─────────────────────────────────────────────────────────┘
```

Each layer assumes the ones below it can fail. Losing TLS exposes control
traffic but not VPN traffic; losing the control plane exposes identity but not
traffic; losing a relay exposes metadata but not traffic.

---

## Four kinds of confidentiality

These get conflated constantly, and conflating them is how people end up
believing a VPN protects something it does not.

| | What it protects | Who can break it |
| --- | --- | --- |
| **Transport confidentiality** | VPN traffic between devices | Nobody, without a device's private key |
| **Control-plane confidentiality** | Enrolment, sessions, configuration | Anyone who breaks TLS, or takes the server |
| **Identity** | Who a user or device is | The control plane, or the identity provider |
| **Device trust** | Whether a device is what it claims | The control plane, and the device's own OS |

The one that matters most: **transport confidentiality does not depend on the
control plane.** The server can be compromised entirely and still cannot read
traffic, because it holds no key that could.

---

## Transport

### Control plane: TLS

All control traffic is TLS. In production the server refuses to start with a
plain-HTTP `base_url` unless the operator explicitly sets
`server.allow_insecure_http` — which exists only for TLS terminated by a proxy
the server cannot see. **This check is implemented and tested today.**

TLS itself is normally terminated by a reverse proxy; see
[deployment](../deployment.md).

The client will have **no flag to skip certificate verification**. Such a flag
is used far more often to paper over a misconfiguration than for its intended
purpose, and it turns an active network attacker into a full compromise. An
operator with a private CA installs the CA.

### Data plane: WireGuard

Traffic between devices is WireGuard, with its own key exchange, forward
secrecy, replay protection and authenticated encryption. Headnet adds nothing
to it and takes nothing away.

Everything Headnet does is *around* WireGuard: deciding which peers exist,
what addresses they have, and which may reach which. See
[ADR-0002](../architecture/decisions/ADR-0002-use-wireguard.md).

---

## Identity (Phase 1)

An authentication abstraction with two implementations rather than one per
vendor — Google, GitHub, Microsoft and Apple are all OIDC, and writing four
integrations would be four times the surface for the same behaviour.

```
AuthProvider
├── Local   email + password, Argon2id
└── OIDC    any compliant issuer
```

Sessions are **server-side**, in a `Secure` `HttpOnly` `SameSite=Lax` cookie.
Deliberately not a stateless JWT: when a laptop is stolen, an administrator
must be able to revoke access *now*, and a self-contained token cannot be
revoked before it expires. The performance argument for stateless tokens does
not apply to a control plane handling tens of requests per second.

Device identity is separate from user identity. Authenticating as a user does
not put a device on the network; enrolment is its own step, with its own audit
trail.

---

## Authorization (Phase 6)

**Default deny.** An empty policy means no traffic.

Enforcement is in *peer distribution*, not in the client. A device that may
not reach another is never sent that peer's configuration — so the restriction
holds even if the client is malicious, which client-side filtering could never
guarantee.

`AllowedIPs` on each WireGuard peer is the second enforcement point: it bounds
what a peer may even send.

---

## Application controls

These are the ones that exist today.

### Rate limiting — implemented

A per-client token bucket, applied **before routing and before
authentication**. Login, enrolment and protocol negotiation are necessarily
unauthenticated, so a limiter that only ran for authenticated callers would
protect nothing that matters.

Buckets are swept periodically: an unbounded map keyed by client address is
itself a memory-exhaustion vector.

**Known gap.** The key is the transport peer address. Forwarding headers are
deliberately ignored, because trusting them would let an attacker reset the
bucket by rotating a header — but behind a reverse proxy that means every
client shares the proxy's bucket. Configure per-client limits at the proxy
until trusted-proxy support lands in Phase 12.

### Input validation — implemented

- JSON decoding rejects unknown fields, so a misspelled security-relevant
  field fails loudly instead of silently taking a default
- Exactly one JSON value per request body
- Request bodies capped
- Configuration validated exhaustively at start-up, reporting every problem at
  once rather than one restart at a time

### Security headers — implemented

`X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
`Referrer-Policy: no-referrer`, a strict `Content-Security-Policy`
(`default-src 'none'`), and cross-origin isolation headers.

HSTS is sent **only** when `base_url` is HTTPS. Sending it from a plain-HTTP
development server would pin a developer's browser to HTTPS on localhost,
which is tedious to undo and confusing to diagnose.

### Error handling — implemented

Every failure returns a stable machine-readable code and a message written for
an operator. Internal detail — stack traces, driver errors, file paths, the
database DSN — never reaches a response. A request ID correlates the response
with the server log.

### Logging — implemented

Structured, with request IDs propagated through the context so a handler
cannot forget to include one.

**Query strings are never logged.** OIDC callbacks and enrolment flows carry
codes and tokens as query parameters, and logs are routinely shipped somewhere
with weaker access controls than the database.

Inbound `X-Request-Id` headers are ignored rather than adopted: that is
attacker-controlled text that would otherwise be written verbatim into every
log line for the request, which is a log-injection and log-forgery vector.

### Timeouts — implemented

Read, write and idle timeouts, plus a tighter header-read timeout so a
slowloris client cannot hold a connection for the full read window. Graceful
shutdown bounded, so an interrupted enrolment does not leave a half-written
record.

### Panic recovery — implemented

One malformed request must not take down a control plane a whole network
depends on. Stack traces go to the log; the client gets a request ID and
nothing else.

### Audit logging (Phase 6)

Every security-relevant action: actor, action, target, source address, result,
timestamp. Never key material, passwords or tokens.

---

## Platform hardening

### Container — implemented

Distroless static base: no shell, no package manager, no libc. Non-root
(UID 65532), read-only root filesystem, all capabilities dropped,
`no-new-privileges`. There is very little for an attacker who achieves code
execution to pivot into.

The healthcheck uses the shipped `headnet` binary rather than curl, which the
image deliberately does not contain.

### systemd — implemented

The unit applies `ProtectSystem=strict`, `PrivateDevices`, `NoNewPrivileges`,
`MemoryDenyWriteExecute`, an empty capability bounding set, a syscall filter,
and `RestrictAddressFamilies=AF_INET AF_INET6`. The service can write to one
directory and speak TCP; everything else is taken away.

### Client daemon (Phase 2)

The daemon will be the only privileged component, because it is the only one
that must touch network interfaces and key material. The CLI and desktop UI
are unprivileged clients of its local API — a boundary that keeps a UI bug
from becoming a root compromise.

The local API will be a unix socket with filesystem permissions, or a named
pipe with an ACL on Windows. **Never a TCP port**: a TCP local API is
reachable by every process on the machine and, on a shared host, by every
user.

---

## Supply chain — implemented

Three direct Go dependencies, each documented with its justification in
[dependencies.md](../development/dependencies.md). Everything on the request
path is code this project is responsible for.

CI runs `govulncheck` (call-graph aware, so a finding means the vulnerable
function is actually reachable), CodeQL, dependency review that blocks
vulnerable or incompatibly-licensed additions, and secret scanning over the
full history. Dependabot updates are grouped weekly, because a wall of
individual bumps gets rubber-stamped — and rubber-stamping dependency updates
is how supply-chain compromises get merged.

---

## Secure defaults

A default that has to be tightened is a default that mostly stays loose. So:

| Setting | Default | Why |
| --- | --- | --- |
| Rate limiting | **On** | The exposed surface is unauthenticated |
| Metrics endpoint | **Off** | Unauthenticated when on |
| Plain HTTP in production | **Refused** | Credentials in the clear |
| Unknown config keys | **Rejected** | A typo must not silently take a permissive default |
| Access policy | **Deny** | A policy bug that denies is an inconvenience; one that allows is a breach |
| Container user | **Non-root** | Nothing here needs root |
| TLS verification | **Always** | No opt-out, ever |

---

## Verification

Security properties are tested, not asserted. Examples that exist today:

- `TestHealthDoesNotLeakInternals` — paths, instance IDs and driver names stay
  out of an unauthenticated response
- `TestRequestLoggerOmitsTheQueryString` — an OIDC code cannot reach a log
- `TestRateLimitMiddlewareCannotBeEvadedWithHeaders` — rotating
  `X-Forwarded-For` does not reset the bucket
- `TestRedactedOutputContainsNoSecretMaterial` — no secret survives
  configuration rendering
- `TestPlainHTTPIsRefusedInProduction` — the transport check holds
- `TestRecovererDoesNotLeakInternals` — a panic discloses nothing
- `TestUnimplementedCommandsFailHonestly` — nothing claims success it did not
  earn

Where a test encodes a security property, it says so in a comment, so that a
future contributor knows why it must not be "simplified" away.
