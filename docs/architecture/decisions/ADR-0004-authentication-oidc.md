# ADR-0004: OIDC-based authentication abstraction

**Status:** Accepted
**Date:** 2026-08-29

## Context

Headnet needs to support local accounts and, eventually, sign-in with Google,
GitHub, Microsoft, Apple and arbitrary corporate identity providers.

The naive approach is an integration per provider. That is five or six code
paths, five or six sets of credential handling, and five or six places for an
authentication bug to hide.

## Decision

One `AuthProvider` abstraction with **two** implementations:

```
AuthProvider
├── Local   email + password, Argon2id
└── OIDC    any compliant issuer
```

Google, GitHub Enterprise, Microsoft Entra, Okta, Keycloak, Authentik and
Dex are all OIDC issuers. They are configuration, not code.

Sessions are **server-side**, carried in a `Secure`, `HttpOnly`,
`SameSite=Lax` cookie.

## Alternatives

**A bespoke integration per provider.** Rejected. It multiplies the
security-critical surface for no functional gain, and each integration is
another thing to keep current as a vendor changes their endpoints.

**Plain OAuth 2.0 rather than OIDC.** OAuth 2.0 is an authorisation framework;
using it for authentication requires deciding for yourself how to obtain and
validate an identity, which is a well-known source of vulnerabilities. OIDC is
the standard that already answers those questions.

**Stateless JWT sessions.** Rejected deliberately, and this is the part of the
decision most likely to be questioned.

JWTs cannot be revoked before they expire. When a laptop is stolen, an
administrator must be able to cut off access *now* — not in fifteen minutes.
The usual workaround is a revocation list, which is a session store with extra
steps and worse ergonomics. The performance argument for stateless tokens does
not apply to a control plane serving tens of requests per second.

**Delegating everything to an external identity provider.** Rejected because
Headnet must work with no internet connection and no third-party account. A
local provider is the baseline; OIDC is the addition.

## Consequences

**Good.**

- Adding a provider is configuration, not a release.
- One code path to review, test and get right.
- Sessions are revocable immediately, which is what device theft demands.
- Works fully offline with local accounts.

**Bad.**

- OIDC has to be implemented carefully: issuer, audience and nonce validation,
  PKCE, and state handling. Getting any of these wrong is a real
  vulnerability, and the standard being well-specified does not make it
  automatic.
- A session store means state, and a database read per authenticated request.
  Cheap at this scale.
- Providers that are not OIDC-compliant would need special handling. None of
  the targeted ones are in that category.

**Constraints this imposes.**

- Local passwords use Argon2id with per-password salts, and the parameters are
  stored alongside the hash so they can be raised later without invalidating
  existing passwords.
- Cookie authentication means CSRF protection is mandatory on every
  non-idempotent request.
- Login responses must be identical for existing and non-existent accounts, in
  content and in timing, or the endpoint becomes a user-enumeration oracle.
- **Authenticating as a user does not put a device on the network.** Device
  enrolment is a separate step with its own audit trail, so a stolen session
  is not automatically network access.
