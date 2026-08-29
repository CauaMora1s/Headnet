# Architecture Decision Records

An ADR records a decision that was expensive to make and would be expensive to
reverse: the context it was made in, what was chosen, what was rejected, and
what the project now has to live with.

They are written when the decision is made, not afterwards. A record produced
later is a rationalisation of whatever was built.

ADRs are immutable once accepted. A decision that turns out to be wrong gets a
**new** ADR that supersedes the old one, and the old one stays — the reasoning
that led somewhere wrong is often the most useful thing in the file.

## Format

```markdown
# ADR-NNNN: Title

**Status:** Proposed | Accepted | Superseded by ADR-XXXX
**Date:** YYYY-MM-DD

## Context      What situation forced a decision?
## Decision     What was chosen?
## Alternatives What else was considered, and why not?
## Consequences What does the project now have to live with?
```

## Index

| | Decision | Status |
| --- | --- | --- |
| [0001](ADR-0001-use-go.md) | Go for the backend and client | Accepted |
| [0002](ADR-0002-use-wireguard.md) | WireGuard as the data plane | Accepted |
| [0003](ADR-0003-monorepo.md) | Monorepo | Accepted |
| [0004](ADR-0004-authentication-oidc.md) | OIDC-based authentication abstraction | Accepted |
| [0005](ADR-0005-database-sqlite-postgres.md) | SQLite and PostgreSQL, pure-Go driver | Accepted |
| [0006](ADR-0006-desktop-wails.md) | Wails for desktop | Accepted |
| [0007](ADR-0007-mobile-flutter.md) | Flutter for mobile | Accepted |
| [0008](ADR-0008-relay-architecture.md) | Relay forwards ciphertext only | Accepted |
| [0009](ADR-0009-licensing.md) | AGPL-3.0 with an Apache-2.0 interop layer | Accepted |
| [0010](ADR-0010-standard-library-http-router.md) | Standard-library HTTP router | Accepted |
| [0011](ADR-0011-cli-standard-library.md) | Standard-library CLI, no framework | Accepted |
