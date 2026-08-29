# ADR-0011: A standard-library CLI, without a framework

**Status:** Accepted
**Date:** 2026-08-29

## Context

Headnet needs a first-class CLI. It will grow to roughly this shape:

```
headnet login | logout | up | connect | disconnect | status
headnet devices | peers | routes | ping <device> | diagnostics | version
headnet admin users | devices | routes | policies
```

That is two levels of nesting, per-command flags, and help output — on
Linux, macOS and Windows.

## Decision

Use the standard library's `flag` package with a small command tree in
`internal/cliutil` (about 250 lines including help rendering).

## Alternatives

**spf13/cobra.** The de facto standard, used by kubectl, Docker and Hugo.
Excellent, and the obvious choice for a large CLI. Rejected here because
Headnet's needs are met by considerably less code than cobra's feature set
implies, and because it pulls in `pflag` and `mousetrap` for functionality
this project does not use. It remains the migration target if the CLI grows
past what the small tree handles comfortably.

**urfave/cli.** Lighter than cobra and pleasant to use. The same argument
applies with less force.

**Bare `flag` with a `switch` on `os.Args[1]`.** Where this would have ended
up without a small abstraction. Rejected because two levels of nesting,
consistent help and consistent exit codes are exactly the things a hand-rolled
switch gets wrong, and inconsistency in a CLI is user-visible.

## Consequences

**Good.**

- No dependencies in the tool an operator runs most often.
- Commands are declarative, so the tree in `apps/cli/main.go` reads as
  documentation of the interface.
- Streams are injected (`cliutil.IO`), so command behaviour is asserted in
  tests rather than eyeballed in a terminal — including that unimplemented
  commands write to stderr and not stdout.
- Exit codes are a first-class, documented part of the contract. A CLI that
  only ever exits 0 or 1 cannot be scripted against.

**Bad.**

- No shell completion. Cobra provides it for free; here it would have to be
  written. This is the most likely reason to revisit.
- Go's `flag` uses single-dash flags (`-json`) rather than GNU-style
  (`--json`), which some users find unfamiliar. Go tooling has normalised it,
  and `--json` is also accepted by the `flag` package.
- No flag grouping or advanced help formatting.
- Roughly 250 lines to maintain and test that a library would have provided.

**Constraints this imposes.**

- The exit codes are part of the public contract and are documented in
  [docs/user-guide/cli.md](../../user-guide/cli.md):

  | | |
  | --- | --- |
  | `0` | Success |
  | `1` | The command ran and failed |
  | `2` | Usage error |
  | `3` | The feature is not implemented yet |
  | `4` | A dependency (daemon, server) was unreachable |

  Code `3` exists specifically so a script can distinguish "this build does
  not have that feature" from "that failed" — which matters while most of the
  CLI is unbuilt.

- Errors go to stderr, output to stdout. Nothing else is acceptable for a tool
  meant to be piped.

## Revisiting this

Move to cobra if shell completion becomes a real request, if the command tree
grows past three levels, or if help formatting starts needing special cases.
Because commands are already declarative structs, that migration is mechanical.
