# ADR-0005: SQLite and PostgreSQL, with a pure-Go driver

**Status:** Accepted
**Date:** 2026-08-29

## Context

Headnet stores users, devices, public keys, address allocations, policy and
audit records. Two very different deployments have to work:

- Someone running it on a Raspberry Pi or a NAS, who should not have to
  install and operate a database server.
- An organisation running it for hundreds of devices, who wants replication,
  backups and the operational tooling they already have.

There is a second constraint that turns out to dominate the driver choice:
[ADR-0001](ADR-0001-use-go.md) commits to `CGO_ENABLED=0` so that one CI job
can produce static binaries for seven platforms.

## Decision

Support **SQLite** (the default) and **PostgreSQL**, both through
`database/sql`.

Use **`modernc.org/sqlite`** — a pure-Go translation of SQLite — rather than
the more common cgo binding, and **`jackc/pgx/v5`** via its `database/sql`
adapter for PostgreSQL.

Hold the SQLite connection pool to a single connection.

## Alternatives

**SQLite only.** Simplest, and enough for most self-hosted installations.
Rejected because it forecloses larger deployments and the operational
practices they need, and retrofitting a second backend after the repositories
are written is far more expensive than allowing for one now.

**PostgreSQL only.** Rejected outright: requiring a database server
contradicts the project's premise. "Run one binary" is the product.

**`mattn/go-sqlite3`.** The most widely used SQLite driver, and faster than
the pure-Go one. Rejected because it requires cgo, which would mean a C
cross-compilation toolchain for every target platform, and would rule out the
distroless container image. The performance difference is irrelevant at the
query volumes a control plane sees; the build simplicity is not.

**An ORM.** Rejected. The queries here are simple, an ORM obscures what
actually reaches the database, and the abstraction usually leaks precisely
where the two dialects differ — which is the one place we need to see clearly.

**Multiple SQLite connections.** Rejected for now. SQLite serialises writers,
and a multi-connection pool produces intermittent "database is locked"
failures under concurrent writes. One connection removes the failure mode
entirely.

## Consequences

**Good.**

- The default installation is a single file. Backing up the control plane is
  copying it.
- `CGO_ENABLED=0` builds work everywhere, which gives static binaries, trivial
  cross-compilation, and a distroless container with no libc.
- PostgreSQL is available for deployments that need it, with no application
  changes beyond configuration.
- Both dialects are exercised in CI, so the second backend does not quietly
  rot.

**Bad.**

- `modernc.org/sqlite` is slower than the cgo binding and is a much larger
  dependency (it brings a translated libc). Both accepted for the build
  properties.
- Two dialects means two sets of migrations, kept in step by a test that fails
  if one gains a migration the other lacks.
- **The single SQLite connection serialises reads behind writes.** This is a
  real limitation, documented in the
  [threat model](../../security/threat-model.md#7-known-limitations), and
  revisited in Phase 12 — most likely with a separate read-only pool.

**Constraints this imposes.**

- SQL is written with `?` placeholders and rebound for PostgreSQL, so
  repositories stay backend-agnostic.
- Dialect-specific SQL is confined to `internal/storage`.
- Migrations are embedded and checksummed. Editing an applied migration is
  refused rather than reconciled: that is how two deployments of the same
  version quietly end up with different schemas.
- A database migrated by a newer build is also refused. Running an old binary
  against a schema it has never seen corrupts data, and failing to start is
  the correct response.
- SQLite pragmas are set explicitly — `foreign_keys` in particular, which
  SQLite leaves off by default and without which deleting a user would
  silently orphan its devices.
