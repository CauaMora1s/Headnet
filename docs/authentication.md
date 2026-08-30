# Authentication

> **Partly implemented.** Accounts and password storage exist and are
> described below as built. Sessions, login, first-run bootstrap and OIDC are
> still being built; each section says which.

---

## Two different questions

Headnet separates them deliberately, and conflating them is a common mistake:

| | Question | Answered by |
| --- | --- | --- |
| **User authentication** | Who is this person? | A password, or an identity provider |
| **Device identity** | Which machine is this? | A key generated on that machine |

**Authenticating as a user does not put a device on the network.** Enrolment
is its own step, with its own audit trail. A stolen browser session is
therefore not automatically network access — the attacker still has to enrol a
device, which is visible.

---

## Providers

One abstraction, two implementations:

```
AuthProvider
├── Local   email + password, Argon2id
└── OIDC    any compliant issuer
```

Google, GitHub Enterprise, Microsoft Entra, Okta, Keycloak, Authentik and Dex
are all OIDC issuers. They are **configuration, not code**. Writing a bespoke
integration per vendor would multiply the security-critical surface for no
functional gain — see
[ADR-0004](architecture/decisions/ADR-0004-authentication-oidc.md).

```yaml
auth:
  providers: [local, oidc]
```

At least one provider is required.

---

## Local accounts

**Implemented.**

For deployments with no identity provider, or none they want to depend on. A
Headnet installation must work with no internet connection and no third-party
account.

**Password storage:**

- **Argon2id**, with parameters from current OWASP guidance
- A per-password salt from the CSPRNG
- **Parameters stored alongside the hash**, so they can be raised later
  without invalidating existing passwords
- Constant-time comparison
- Plaintext never stored, never logged, never returned by any API

A password is bounded at both ends. The minimum (12 characters) is a floor
rather than a composition policy: length beats the digit-and-symbol rules that
mostly produce `Password1!`. The maximum (1024) is a denial-of-service control,
because hashing an unbounded input is an easy way to burn a server's memory
bandwidth.

The stored hash never leaves the `User` type. The field is unexported, so a
JSON encoder or a template cannot reach it, and the type implements `String`
and `GoString`, so a `%+v` in a log call or an error message cannot either —
`fmt` reads unexported fields quite happily, and the unexported field alone
would not have been enough.

**Login behaviour** (being built):

- Responses are identical for existing and non-existent accounts, in content
  *and in timing*. Otherwise the endpoint is a user-enumeration oracle. The
  hashing package provides `DummyHash` for exactly this: an attempt against an
  address with no account still does a full Argon2 verification.
- Rate limits tighter than the global limiter, plus account lockout with
  backoff.

---

## OIDC

Standard authorisation-code flow with PKCE.

```yaml
auth:
  providers: [oidc]
  oidc:
    issuer: "https://accounts.google.com"
    client_id: "..."
    # client_secret comes from HEADNET_AUTH_OIDC_CLIENT_SECRET
    scopes: [openid, email, profile]
```

The client secret is supplied through the environment, never the YAML file.

**What has to be right**, because these are where OIDC implementations go
wrong:

- Issuer validation against the discovery document
- Audience validation — the token must be for *this* client
- Nonce validation, to bind the token to this login attempt
- `state`, to prevent CSRF on the callback
- PKCE, so an intercepted authorisation code is useless
- Signature verification against the provider's JWKS, with key rotation
- Expiry and clock-skew handling

The standard being well-specified does not make any of this automatic.

The redirect URI is built from `server.base_url`, which is why that setting
must be the URL users actually reach — not the bind address.

---

## Sessions

Server-side, in a cookie:

```
Set-Cookie: headnet_session=...; Secure; HttpOnly; SameSite=Lax; Path=/
```

**Deliberately not a stateless JWT.** When a laptop is stolen, an
administrator must be able to cut off access *now* — not in fifteen minutes
when a token happens to expire. The usual workaround, a revocation list, is a
session store with extra steps and worse ergonomics. The performance argument
for stateless tokens does not apply to a control plane serving tens of
requests per second.

Cookie authentication means **CSRF protection is mandatory** on every
non-idempotent request: a double-submit token, checked server-side.

---

## First-run bootstrap

The window between "the server starts" and "an administrator exists" is a real
vulnerability if it is handled carelessly — an unclaimed installation on the
public internet is an open invitation.

The design:

- The first account can be claimed exactly **once**, atomically, so two
  simultaneous attempts cannot both succeed.
- Until it is claimed, the server serves nothing but the bootstrap flow.
- The bootstrap endpoint is rate-limited like everything else.
- Claiming it is audited.

---

## Device enrolment

### Interactive

```
Install → headnet login → the device appears → connect
```

The user signs in through their browser; the daemon generates a key pair
locally and registers the public half.

### Headless, with a setup key

For a server, a container, or a machine being provisioned by a script:

```bash
headnet up --setup-key hk_...
```

A setup key is a credential and is treated as one:

| Property | Why |
| --- | --- |
| **Shown once**, stored hashed | Reading the database yields nothing usable |
| **Revocable** | A leak has a bounded response |
| **Expiring by default** | No-expiry has to be chosen deliberately |
| **Scoped** | A key may never grant more than its issuer holds |
| **Optionally single-use** | The tightest option for one-off provisioning |
| **Audited** | Creation, every use with its source address, and revocation |

Operationally: treat one like a password. Do not commit it, do not paste it
into chat, do not leave it in shell history. Prefer short-lived single-use
keys.

### QR enrolment — Phase 10

For mobile. Short-lived, single-use and scoped — a QR code is trivially
photographed over someone's shoulder.

---

## MFA — Phase 11

TOTP for local accounts, and WebAuthn where it can be supported. For OIDC,
MFA is the provider's responsibility, which is one of the arguments for using
one.

---

## Revocation

Two separate things, and both matter:

**Revoking a session** signs a user out of the web UI. Immediate, because
sessions are server-side.

**Revoking a device** removes it from the network. This must *disconnect*, not
merely delist: peers are told to drop it and the tunnel dies. A device removed
from a list whose peers still hold its configuration is still on the network.

---

## What the server stores

| | |
| --- | --- |
| Argon2id password hashes | For local accounts |
| OIDC subject identifiers | For federated accounts |
| Session records | Revocable |
| Setup key hashes | Never the keys themselves |
| Device **public** keys | Never private keys |

Nothing here can decrypt VPN traffic. That is the point: a stolen database
reveals the shape of the network and is worth attacking offline for the
password hashes, but it yields no plaintext. See
[key management](security/key-management.md).

---

## Further reading

- [ADR-0004](architecture/decisions/ADR-0004-authentication-oidc.md) — why one
  abstraction and why sessions rather than JWTs
- [Threat model](security/threat-model.md) — leaked setup keys, token theft,
  a compromised identity provider
- [Key management](security/key-management.md) — where every secret lives
