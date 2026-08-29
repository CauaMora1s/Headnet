# ADR-0010: The standard-library HTTP router

**Status:** Accepted
**Date:** 2026-08-29

## Context

The control plane needs HTTP routing with method matching and path parameters:

```
GET    /api/v1/devices
POST   /api/v1/devices
GET    /api/v1/devices/{id}
DELETE /api/v1/devices/{id}
```

The project brief suggested chi or a similar lightweight router. The
[dependency policy](../../development/dependencies.md) requires asking whether
the standard library is sufficient first.

Since Go 1.22, it is. `net/http.ServeMux` supports method-and-pattern routing
(`GET /api/v1/devices/{id}`) and wildcards natively.

## Decision

Use `net/http.ServeMux`. Write the middleware — request IDs, panic recovery,
logging, security headers, body limits, rate limiting — in `internal/httpapi`.

## Alternatives

**chi.** Excellent, small, `net/http`-compatible, and the conventional choice.
Rejected because since Go 1.22 it provides almost nothing the standard library
lacks for this application. Its middleware collection is convenient, but the
middleware Headnet actually needs is security-relevant and worth writing and
reviewing directly rather than adopting.

**gorilla/mux.** Mature, but heavier, and it was archived and revived once,
which is a maintenance signal worth weighing for something on the path of
every request.

**gin or echo.** Full frameworks with their own context type and conventions.
Rejected as disproportionate: Headnet needs routing, not a framework, and the
custom context types make handlers harder to test with the standard library
tooling.

**httprouter.** Fast, but no longer the differentiator it was, and it does not
implement `http.Handler` idiomatically.

## Consequences

**Good.**

- Zero dependencies on the path of every request. Nothing in the routing layer
  can be compromised upstream.
- No framework to learn, and no framework-specific idioms in handlers.
- Handlers are plain `http.HandlerFunc`, so they test with `httptest` and
  nothing else.
- Middleware is `func(http.Handler) http.Handler`, which is compatible with
  the wider ecosystem should we ever want a third-party middleware.
- The security-relevant middleware is code this project owns and reviews.

**Bad.**

- Route groups have to be composed by hand rather than with a `Group()`
  helper. In practice that is string concatenation on a prefix.
- No built-in route-level middleware; the chain is applied to the whole mux.
  If per-route middleware becomes necessary — likely when authentication lands
  in Phase 1 — it will be a small wrapper, and that is a good time to
  re-evaluate this decision.
- Some middleware had to be written that a library would have provided. That
  cost has largely been paid, and the result is tested.

**Constraints this imposes.**

- Middleware order is explicit and security-relevant. It is documented at the
  point where the chain is built, in `internal/server`, with the reasoning for
  each position.
- Method matching relies on Go 1.22+ pattern syntax, so the minimum Go version
  cannot drop below that. It is already 1.25 for other reasons.
- Unmatched routes are handled explicitly, so a mistyped path returns the
  standard error envelope rather than Go's plain-text 404.

## Revisiting this

If per-route middleware, route groups or path-parameter handling become a
recurring source of friction once the API surface is large, chi is the
migration target: it implements `http.Handler` and the handlers themselves
would not change.
