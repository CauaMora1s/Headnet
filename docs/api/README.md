# API

The control-plane HTTP API is specified in
[`openapi.yaml`](openapi.yaml) (OpenAPI 3.1).

**The specification describes only what is implemented.** Endpoints for
unbuilt features are deliberately absent rather than present-and-stubbed — a
specification listing endpoints that return fabricated data is worse than one
that is honestly short. Planned endpoints are in
[the roadmap](../ROADMAP.md) and will be added here as they land.

---

## What exists today

| | | |
| --- | --- | --- |
| `GET` | `/health` | Aggregate health with per-dependency detail |
| `GET` | `/live` | Liveness — is the process running? |
| `GET` | `/ready` | Readiness — can it serve real traffic? |
| `GET` | `/metrics` | `404` when disabled, `501` when enabled (Phase 12) |
| `GET` | `/api/v1/version` | Build identity and protocol range |
| `POST` | `/api/v1/protocol/negotiate` | Agree a version and capability set |

Everything else returns `404`. Devices, users, routes and policies do not
exist yet, and an empty list would read as "you have no devices" rather than
"this has not been built".

---

## Reading the specification

```bash
# In a browser
npx @redocly/cli preview-docs docs/api/openapi.yaml

# Validate it
npx @redocly/cli lint docs/api/openapi.yaml
```

Or paste it into [editor.swagger.io](https://editor.swagger.io/).

---

## Conventions

### Versioning

The path carries the API version (`/api/v1`), which changes only for a
breaking change. Additive changes happen in place, which is why **clients must
ignore fields they do not recognise**.

Separately, a *protocol* version governs the client/server contract as a
whole. It is a single integer, with optional behaviour gated on named
capabilities rather than version comparisons. See
[protocol versioning](../architecture/protocol-versioning.md).

### Errors

One envelope for every failure:

```json
{
  "error": {
    "code": "bad_request",
    "message": "protocol_version must be a positive integer",
    "details": { "field": "protocol_version" },
    "request_id": "req_4KQZ9V3B2N7XR8T0YCFHJM"
  }
}
```

- `code` is stable. Branch on it.
- `message` is for humans and may change. Do not parse it.
- `details` carries structured, code-specific context.
- `request_id` also appears in the `X-Request-Id` header, and correlates the
  response with the server log.

The full list of codes and their statuses is in the specification. The ones
worth knowing:

| Code | Status | Meaning |
| --- | --- | --- |
| `not_implemented` | 501 | A declared endpoint with no behaviour behind it yet. `details.roadmap_phase` says when. |
| `unsupported_protocol_version` | 426 | The ranges do not overlap; one side must be upgraded. |
| `rate_limited` | 429 | Retry after `Retry-After` seconds. |
| `unavailable` | 503 | A dependency is down. Retryable. |

`rate_limited`, `unavailable` and `internal` are the retryable ones.

### Request bodies

JSON, with `Content-Type: application/json`. Decoding is strict:

- **Unknown fields are rejected.** A client that misspells a
  security-relevant field learns about it rather than silently getting the
  default.
- Exactly one JSON value per body.
- Bodies are capped by `server.max_body_bytes` (1 MiB by default); exceeding
  it returns `413`.

### Rate limiting

Applied per client, **before** routing and authentication — login, enrolment
and protocol negotiation are necessarily unauthenticated and are exactly what
needs a ceiling. Exceeding the budget returns `429` with `Retry-After`.

Behind a reverse proxy every request appears to come from the proxy, so the
limiter treats it as one client. Forwarding headers are deliberately not
trusted, because honouring them would let an attacker reset the bucket by
rotating a header. See [deployment](../deployment.md).

### Authentication

Not implemented yet ([Phase 1](../ROADMAP.md#phase-1--server-mvp)). It will be
a server-side session in a `Secure`, `HttpOnly`, `SameSite=Lax` cookie, with
CSRF protection on every non-idempotent request.

Deliberately not a stateless token: when a device is stolen, an administrator
must be able to revoke access immediately, and a self-contained token cannot
be revoked before it expires. See
[ADR-0004](../architecture/decisions/ADR-0004-authentication-oidc.md).

---

## Generated clients

None yet. The surface is small enough that the hand-written TypeScript client
in [`apps/web/src/lib/api.ts`](../../apps/web/src/lib/api.ts) is clearer than
generated code, and it avoids making the frontend build depend on the Go
toolchain.

When the surface grows, generation from this specification becomes worth the
machinery.

---

## Keeping this honest

The specification is part of the [Definition of Done](../development/definition-of-done.md):
an endpoint added or changed without updating `openapi.yaml` is not finished.
A stale contract is worse than none, because people trust it.
