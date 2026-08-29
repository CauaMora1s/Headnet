# Security Policy

Headnet is networking software that holds the identity of every device on a
private network. A vulnerability here can expose an entire organisation's
internal services. We take reports seriously and we would rather hear about a
problem from you than from an incident.

## Reporting a vulnerability

**Please do not open a public issue, pull request or discussion for a security
problem.** A public report tells attackers before it tells us, and everyone
running Headnet is exposed in the interval.

Report privately through **[GitHub Security
Advisories](https://github.com/headnet/headnet/security/advisories/new)**. That
gives us a private channel with you, and gives you credit in the advisory when
it is published.

### What to include

The more of this you can provide, the faster the fix:

- What the vulnerability lets an attacker do, in concrete terms.
- The affected version or commit, and the deployment shape (SQLite or
  PostgreSQL, behind a proxy or not).
- Steps to reproduce, ideally a minimal one.
- Any proof-of-concept you have.
- Whether you have disclosed this anywhere else, or intend to.

You do not need a working exploit. A clear description of a design flaw is
just as valuable.

### What to expect

| | |
| --- | --- |
| Acknowledgement | Within **3 working days** |
| Initial assessment | Within **10 working days** |
| Fix or mitigation plan | Communicated with the assessment |
| Public disclosure | Coordinated with you, normally within **90 days** |

If we are going to miss one of these, we will tell you rather than go quiet.

We ask for 90 days before public disclosure so that people running Headnet can
upgrade. If the vulnerability is being actively exploited, we will move much
faster and will say so.

We do not currently run a bug bounty. We do credit reporters in advisories
unless you would rather remain anonymous.

### Safe harbour

If you research Headnet in good faith — testing against your own installation,
not accessing other people's data, not degrading anyone's service, and giving
us reasonable time to respond — we will not pursue or support legal action
against you, and we will work with you on disclosure.

## Supported versions

| Version | Supported |
| --- | --- |
| `main` | Yes |
| Tagged releases | None yet |

Headnet has not made a release. Until it does, `main` is the only supported
tree, and **it should not be used to protect anything that matters**.

## Scope

### In scope

- The control-plane server (`apps/server`) and everything under `internal/`
- Shared protocol and API contracts (`packages/`)
- The CLI (`apps/cli`)
- The web UI (`apps/web`)
- Deployment assets (`deploy/`) where a default is insecure
- Documentation where it recommends something unsafe

### Out of scope

- **Vulnerabilities in WireGuard itself** — report those to the
  [WireGuard project](https://www.wireguard.com/).
- **Features that do not exist yet.** Most of Headnet is unimplemented; a gap
  covered by [the roadmap](docs/ROADMAP.md) is a known absence, not a
  vulnerability. Where a *design* for an unbuilt feature is unsound, that
  absolutely is worth reporting — it is much cheaper to fix now.
- **Missing hardening on a deliberately documented trade-off**, unless you can
  show the documented reasoning is wrong. The known limitations are listed in
  [the threat model](docs/security/threat-model.md#7-known-limitations).
- Findings that require an attacker to already have root on the control-plane
  host or on a client device, beyond what the threat model already covers.
- Automated scanner output with no demonstrated impact.
- Denial of service by brute traffic volume against a self-hosted service.

## Security model in brief

The full analysis is in [docs/security/threat-model.md](docs/security/threat-model.md).
The properties Headnet intends to hold, and which a report may reasonably test:

1. **Private keys never leave a device.** They are generated locally. The
   server stores public keys only, and a server compromise must not yield the
   ability to decrypt traffic.
2. **Relays cannot read traffic.** A relay forwards WireGuard packets it has
   no key for. A compromised relay sees ciphertext, sizes and timing.
3. **Access is default-deny.** A device may reach another only where policy
   explicitly allows it.
4. **A revoked device loses access.** Revocation must actually cut a device
   off, not merely hide it from a list.
5. **Enrolment credentials are constrained.** Setup keys are revocable,
   expirable and scoped; a leaked key must not be equivalent to an
   administrator account.
6. **Nothing secret is logged.** Not private keys, passwords, session tokens,
   setup keys or OIDC client secrets.

If you find a way to break any of these, that is a vulnerability, even if the
feature involved is only partly built.

## For operators

Headnet is pre-release and unaudited. If you are running it anyway:

- Terminate TLS in front of it. The server refuses to start in production with
  a plain-HTTP `base_url` unless you explicitly opt out.
- Keep the control plane's port off the public internet unless it needs to be
  there.
- Back up the database. It *is* the control plane; there is no other copy.
- Supply the PostgreSQL DSN through the environment, not the configuration
  file.
- Leave rate limiting on, and add per-client limits at your reverse proxy —
  behind a proxy, Headnet sees only the proxy's address. See
  [docs/deployment.md](docs/deployment.md).
- Watch [releases](https://github.com/headnet/headnet/releases) for advisories.

## Our commitments

- We will not knowingly ship a default that is insecure for the sake of
  convenience. Where a trade-off exists, it is documented and opt-in.
- We will not invent cryptography. Headnet uses WireGuard, TLS and established
  password hashing, and nothing home-grown.
- Security-relevant changes get an entry in [CHANGELOG.md](CHANGELOG.md), and
  advisories for anything that affects a released version.
