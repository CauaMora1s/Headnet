# ADR-0003: Monorepo

**Status:** Accepted
**Date:** 2026-08-29

## Context

Headnet is at least seven deliverables: a control-plane server, a client
daemon, a CLI, a relay, a web UI, a desktop app and mobile apps. They share a
wire protocol, an API contract, a configuration format and a data model.

The question is whether they live in one repository or several.

## Decision

One repository, laid out as:

```
apps/       programs
packages/   shared contracts (Apache-2.0)
internal/   control-plane implementation (AGPL-3.0)
deploy/     deployment assets
docs/       documentation
test/       cross-component tests
```

## Alternatives

**A repository per component.** The conventional answer, and wrong here. A
protocol change would require a coordinated release across several
repositories, with a window in which they disagree. Version skew between a
server and a client is a real problem this project has to handle at runtime
anyway ([protocol versioning](../protocol-versioning.md)); there is no reason
to also have it at development time.

**Server and clients split in two.** Better, but the protocol package would
still have to be published and versioned separately, and every protocol change
would still be a two-repository dance.

**A monorepo with Go workspaces (multiple modules).** Considered seriously.
Rejected as premature: one module is simpler, and the moment a component
genuinely needs an independent release cadence — most likely the relay —
splitting it out is a mechanical change.

## Consequences

**Good.**

- A protocol change lands atomically across the server and every client. It is
  not possible to merge a server change that breaks a client without the CI
  for both running on the same commit.
- One test suite covers cross-component behaviour.
- One CI pipeline, one issue tracker, one version history.
- A contributor clones once and has everything.
- Documentation lives next to the code it describes, which is the only
  arrangement where it reliably stays current.

**Bad.**

- The repository is larger than any single component needs.
- CI runs more than a given change strictly requires. Acceptable at this size;
  path filters can come later if it becomes slow.
- Independent release cadence per component takes deliberate work.
- Go's `internal/` rule applies to the whole module, which is why the
  interoperability surface had to be separated into `packages/` explicitly
  rather than emerging naturally.

**Constraints this imposes.**

- **Nothing in `packages/` may import from `internal/`.** `packages/` is
  Apache-2.0 and is meant to be reusable by third parties; `internal/` is
  AGPL-3.0 and is not importable outside this module in any case. The
  dependency direction is one-way and must stay that way.
- Directories for unbuilt components (`apps/client`, `apps/relay`,
  `apps/desktop`, `apps/android`, `apps/ios`) contain a placeholder explaining
  what belongs there and when, and no code. An empty directory that looks like
  a component is worse than an honest note.
