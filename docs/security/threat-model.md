# Threat model

This document says what Headnet trusts, what it does not, and what happens
when each part of it is compromised.

It is written before most of the system exists, deliberately. A threat model
produced after the code is a rationalisation of whatever was built; produced
before, it constrains what gets built. Where a control is not implemented yet,
this document says so rather than describing an intention as a fact.

**Current status: Headnet is pre-release and unaudited. Almost every control
described here is planned rather than built. Do not use it to protect anything
that matters.**

---

## 1. What the system is

Headnet has three kinds of component:

| Component | Runs where | Holds |
| --- | --- | --- |
| **Control plane** | An operator's server | Identity, device *public* keys, policy, address assignments |
| **Client daemon** | Each user's device | That device's *private* key, its configuration |
| **Relay** | An operator's server, optionally | Nothing persistent; forwards ciphertext |

The defining split: the control plane decides *who may talk to whom*.
WireGuard, running between devices, decides *what the packets are*. The
control plane never sees plaintext traffic and never holds a key that could
decrypt it.

```
                    ┌────────────────────┐
                    │   Control plane    │  identity, policy,
                    │  (headnet-server)  │  public keys, addresses
                    └─────────┬──────────┘
                              │  TLS  (authenticated)
                ┌─────────────┴─────────────┐
                │                           │
         ┌──────┴──────┐             ┌──────┴──────┐
         │  Device A   │             │  Device B   │
         │ private key │             │ private key │
         └──────┬──────┘             └──────┬──────┘
                │                           │
                │  WireGuard — end-to-end encrypted
                ├────────── direct ─────────┤
                │                           │
                └──────► Relay ◄────────────┘
                    forwards ciphertext,
                    holds no keys
```

---

## 2. What is trusted

Being explicit about this is the point of the exercise. Everything in this
list is a component whose compromise causes real harm.

| Trusted | Why, and how far |
| --- | --- |
| **WireGuard** | For all traffic confidentiality and integrity. Headnet implements no cryptography of its own. |
| **The operating system** of a client device | It holds the private key. A compromised OS is a compromised device; nothing above it can help. |
| **The platform CSPRNG** | For key and identifier generation. |
| **The TLS certificate authority chain** | For control-plane authentication. |
| **The control-plane operator** | Sees who exists, which devices, and the whole policy. Cannot read traffic. |
| **The database** | Holds identity and policy. Read access is enough to enumerate the network. |
| **The identity provider**, when OIDC is used | Can authenticate as any user. |

### Explicitly not trusted

- **The network between components.** Assume it is observed and modified.
- **The relay.** It forwards ciphertext and cannot read it. Compromise leaks
  metadata, not content.
- **Any client device, from the control plane's perspective.** A device may be
  stolen, compromised, or lying about what it is.
- **Anything a client sends.** Hostnames, capabilities, endpoint candidates
  and advertised routes are all attacker-controlled input.
- **STUN servers.** They can lie about a reflexive address; only a completed
  WireGuard handshake confirms a path.
- **The browser environment** of the web UI, beyond ordinary web assumptions.

---

## 3. Who is attacking

| Adversary | Capability | Primary goal |
| --- | --- | --- |
| **Passive network observer** | Reads traffic between components | Learn who talks to whom, and what |
| **Active network attacker** | Modifies, injects, replays | Impersonate a device or the server |
| **Malicious insider** | A valid account and device | Reach services they are not authorised for |
| **Thief** | Physical possession of a device | Use its established access |
| **Compromised relay operator** | Full control of a relay | Read or tamper with relayed traffic |
| **Compromised control-plane operator** | Root on the server | Read traffic, add devices |
| **Opportunistic internet attacker** | Can reach the exposed port | Any foothold at all |

---

## 4. Threats and responses

Each entry gives the threat, the mitigation, and — honestly — whether that
mitigation exists yet.

### 4.1 Malicious or compromised control-plane administrator

**Threat.** Someone with root on the control-plane server wants to read
traffic on the network.

**What they can do.** Enumerate every user, device and policy. Change policy.
Enrol a *new* device of their own and authorise it to reach services — which
is the real attack, and it is not preventable by a system that lets an
administrator add devices at all.

**What they cannot do.** Decrypt existing traffic. Private keys are on
devices; the server holds public keys. There is no stored value that yields
plaintext, and this is the single most important property of the design.

**Mitigations.** Public keys only, never private (planned, Phase 1–2). Audit
logging of every enrolment and policy change, so an added device is visible
after the fact (planned, Phase 6). Device enrolment surfaced in the UI so
users can see what is on their network.

**Residual risk.** An administrator can add themselves to the network. This is
inherent: they run the network. The defence is that it is *visible*, not that
it is impossible. Users who need protection from their own network operator
need end-to-end encryption at the application layer as well.

### 4.2 Compromised client device

**Threat.** Malware or an attacker with root on a member device.

**What they can do.** Read that device's private key and impersonate it for as
long as it stays enrolled. Reach whatever that device is authorised to reach.

**Mitigations.** Default-deny policy limits the blast radius to what that
device was permitted (planned, Phase 6). Revocation cuts it off (planned,
Phase 3 — and revocation must *disconnect*, not merely delist). Key files
stored 0600 in a root-owned directory (planned, Phase 2). Key rotation
(Phase 11).

**Residual risk.** Full. A compromised device is compromised; Headnet limits
scope, it does not restore integrity.

### 4.3 Stolen device

**Threat.** A laptop or phone is taken.

**Mitigations.** One-click revocation, taking effect in seconds (planned,
Phase 3). Sessions are server-side and revocable — this is precisely why
Phase 1 uses sessions rather than stateless JWTs, which cannot be revoked
before expiry. Audit trail of what the device did.

**Residual risk.** Anything the thief did between theft and revocation. Disk
encryption on the device is the user's responsibility and is recommended in
the documentation.

### 4.4 Leaked enrolment (setup) key

**Threat.** A setup key ends up in a shell history, a CI log, a screenshot or
a chat message.

**What an attacker gains.** The ability to enrol a device, with whatever
access the key's scope grants.

**Mitigations (all planned, Phase 1).** Keys are revocable; expire by default;
carry a scope narrower than an administrator; may be single-use; are stored
hashed so reading the database does not yield usable keys; and every use is
audited with the source address.

**Residual risk.** The window between leak and revocation. Short expiry and
single-use keys are the main defence, and the documentation will push both.

### 4.5 Compromised relay

**Threat.** An attacker controls a relay, or operates a malicious one.

**What they can do.** Observe that two peers communicate, and when, how much
and how often. Drop or delay traffic — a denial of service.

**What they cannot do.** Read or forge traffic. Relayed packets are WireGuard
ciphertext; the relay holds no key and injecting a forged packet fails
WireGuard's authentication.

**Mitigations (planned, Phase 5).** Relays never receive WireGuard keys.
Relay access requires a short-lived, audience-restricted token. Clients report
when they are relaying, so a user knows their traffic is taking a longer path.

**Residual risk.** Traffic-metadata analysis. This is inherent to relaying and
is documented rather than papered over. Users who need to defend against
traffic analysis need something Headnet does not attempt to provide.

### 4.6 Compromised identity provider

**Threat.** The OIDC provider is compromised, or is malicious.

**What they can do.** Authenticate as any user of that provider.

**Mitigations (planned, Phase 1 and 11).** Strict issuer, audience and nonce
validation. Device enrolment as a second step, so an authenticated session is
not itself network access. MFA where the provider supports it. Support for
more than one provider, so a single one is not a total dependency.

**Residual risk.** Substantial. Delegating authentication means trusting the
delegate. Operators who cannot accept that should use local accounts.

### 4.7 Replay attacks

**Threat.** Captured requests or handshakes are replayed.

**Mitigations.** WireGuard has replay protection built in — this is exactly
the class of problem that is solved by not inventing your own protocol. TLS
protects the control plane. Session tokens are bound server-side. Enrolment
requests carry a nonce and are single-use (planned, Phase 2).

### 4.8 Token and session theft

**Threat.** A session cookie or API token is stolen through XSS, a malicious
extension, or a leaky log.

**Mitigations (planned, Phase 1).** `HttpOnly`, `Secure`, `SameSite=Lax`
cookies. A strict Content-Security-Policy — already applied to the API today.
Server-side sessions that can be revoked immediately. No token ever written to
a log, and query strings excluded from request logging entirely, because OIDC
callbacks carry codes in them (**implemented today**).

### 4.9 Privilege escalation

**Threat.** A user or device gains access it was not granted.

**Mitigations.** Default deny (planned, Phase 6). Authorisation checked at the
point of peer distribution, not in the client — a client is never sent the
configuration for a peer it may not reach. Role separation between user and
administrator. Setup-key scopes that cannot exceed their issuer's own
permissions.

### 4.10 Unauthorised route advertisement

**Threat.** A compromised device advertises `0.0.0.0/0` and becomes a man in
the middle for the whole network.

**Mitigations (planned, Phase 8).** Advertisement is not approval: an
administrator approves every route before it is distributed. Overlap and
conflict detection. Route use is subject to policy like everything else.

**This threat is why route approval is a hard requirement and not a
convenience feature.**

### 4.11 Unauthorised device enrolment

**Threat.** An attacker enrols a device they should not.

**Mitigations (planned, Phase 1).** Enrolment requires an authenticated
session or a valid setup key. Rate limiting on the enrolment endpoint (the
global limiter exists **today**). Every enrolment audited and surfaced in the
UI. Optional administrator approval before a device becomes active.

### 4.12 Malicious peer

**Threat.** An authorised device attacks another device it can reach.

**Mitigations.** Least-privilege policy (Phase 6). `AllowedIPs` restricts what
a peer may even send. Headnet does not attempt host-level protection —
firewalls and patching remain the device owner's job, and the documentation
says so.

### 4.13 API abuse

**Threat.** Automated scraping, enumeration or resource exhaustion.

**Mitigations (**implemented today**).** Per-client token-bucket rate limiting,
applied before routing and before authentication. Request body size limits.
Strict JSON decoding that rejects unknown fields. Bounded server timeouts,
with a tighter header-read timeout against slowloris. Health endpoints that
disclose nothing beyond a release string.

**Known gap.** Behind a reverse proxy every request appears to come from the
proxy, so the limiter treats the proxy as one client. Forwarding headers are
deliberately not trusted, because trusting them lets an attacker reset the
bucket by rotating a header. Until trusted-proxy support lands (Phase 12),
per-client limits must be configured at the proxy. See
[deployment](../deployment.md).

### 4.14 Brute-force authentication

**Threat.** Password guessing, or credential stuffing.

**Mitigations (planned, Phase 1).** Argon2id, which is expensive by design.
Tighter rate limits on authentication than elsewhere. Account lockout with
backoff. Login responses that are identical for existing and non-existent
accounts, in both content and timing.

### 4.15 WebSocket abuse

**Threat.** The event channel is used to exhaust server resources.

**Mitigations (planned, Phase 3).** Authenticated at handshake. Per-connection
message rate and size limits. A cap on concurrent connections per device.
Idle timeouts.

### 4.16 DNS abuse

**Threat.** Internal names leak, are hijacked, or DNS is used to exfiltrate
data.

**Mitigations (planned, Phase 7).** Names assigned by the server, not claimed
by devices. Internal names never forwarded to public resolvers. Query rate
limiting.

### 4.17 Supply-chain compromise

**Threat.** A malicious dependency, or a compromised build.

**Mitigations (**implemented today**).** Three direct Go dependencies, all
documented. `go.sum` verification in CI. `govulncheck`, CodeQL, dependency
review and secret scanning in the security workflow. Grouped Dependabot
updates so that reviewing them stays meaningful rather than becoming a
rubber-stamp. Reproducible builds with `-trimpath`. A distroless container
image with no shell to pivot into.

---

## 5. What happens if...

### ...the control-plane server is compromised?

The attacker gains the identity database, policy, and the ability to enrol
devices and change rules. **They do not gain the ability to decrypt traffic**,
past or present: no private key is stored there.

Recovery: restore from backup on clean infrastructure, revoke every device,
re-enrol. Because the instance ID changes with a rebuilt database, clients
that pinned the old one will refuse to reconnect silently — which is the
intended behaviour, not a bug.

### ...the database is stolen?

The attacker learns the shape of the network: users, devices, public keys,
addresses, policy. Passwords are Argon2id hashes; setup keys are stored
hashed. They cannot decrypt traffic and cannot impersonate a device, because
the private keys are not there.

### ...a device is stolen?

Revoke it. Its tunnels drop within seconds and its sessions are invalidated.
The exposure is whatever happened before revocation.

### ...a setup key leaks?

Revoke it, and audit what enrolled with it. Prefer short-lived, single-use,
narrowly scoped keys so that the answer is usually "nothing".

### ...a relay is compromised?

The attacker sees which peers communicate and when. They cannot read the
traffic. Point clients at a different relay.

### ...TLS is stripped or intercepted?

Control-plane traffic — enrolment, sessions, configuration — is exposed. This
is why the server refuses to start in production with a plain-HTTP
`base_url` unless the operator explicitly opts out, and why the client will
have no flag to skip certificate verification.

Note that even a fully intercepted control plane does not yield traffic
plaintext, because the WireGuard keys are not in it.

---

## 6. Security properties Headnet intends to hold

These are testable claims. A way to break any of them is a vulnerability worth
reporting under [SECURITY.md](../../SECURITY.md), even where the feature is
only partly built.

1. A private key never leaves the device that generated it.
2. The control plane cannot decrypt VPN traffic.
3. A relay cannot decrypt VPN traffic.
4. A device receives configuration only for peers it is authorised to reach.
5. Revocation disconnects, promptly.
6. Access is denied unless a policy allows it.
7. Setup keys are revocable, expirable and scoped.
8. No secret is ever written to a log.
9. An unimplemented feature never reports success.

Property 9 is a security property, not a style preference: a VPN client that
claims to be connected when it is not can lead someone to send sensitive
traffic over a network they believe is protected.

---

## 7. Known limitations

Accepted, deliberately, for now.

| Limitation | Consequence | Resolution |
| --- | --- | --- |
| Rate limiting keys on the transport peer address | Behind a proxy, all clients share one bucket | Trusted-proxy support, Phase 12 |
| No traffic-analysis resistance | A relay operator sees timing and volume | Inherent to relaying; documented, not fixed |
| Administrators can enrol devices | An operator can join their own network | Inherent; mitigated by audit visibility |
| SQLite holds one connection | Reads serialise behind writes | Acceptable at the target scale; Phase 12 |
| No HA for the control plane | Downtime stops enrolment and config changes | Phase 12. Existing tunnels keep working. |
| Metrics endpoint unauthenticated when enabled | Operational data exposure | Off by default; bind privately |
| No formal security audit | Unknown unknowns | Phase 12 |

Existing tunnels surviving a control-plane outage is worth emphasising: the
control plane is not on the data path, so if it goes down, established
connections continue. What stops is enrolment, revocation and policy changes.

---

## 8. Reviewing this document

This model is revisited at the start of every roadmap phase, because each one
adds attack surface. A phase is not complete until its security considerations
are reflected here.

Last reviewed: Phase 0.
