# ADR-0001: Go for the backend and client

**Status:** Accepted
**Date:** 2026-08-29

## Context

Headnet needs one language for a control-plane server, a client daemon that
manipulates network interfaces on Linux, macOS and Windows, a CLI, and
eventually a relay. The client in particular has hard constraints:

- It must run as a background service on all three desktop platforms.
- It must be small enough for a Raspberry Pi and other ARM devices.
- It must be distributable as a single file. Asking a user to install a
  runtime before they can join a network defeats the purpose of the project.
- It must talk to WireGuard, whose reference userspace implementation and most
  mature control libraries are written in Go.

## Decision

Go, for the server, the client daemon, the CLI and the relay.

## Alternatives

**Rust.** Stronger memory-safety guarantees and excellent performance.
Rejected because the Go ecosystem around WireGuard is substantially more
mature — `wireguard-go` and `wgctrl-go` are the reference tools — and because
Go's cross-compilation is a genuine advantage for a project that must ship
seven platform binaries from one CI job. Garbage collection is irrelevant at
the request volumes a self-hosted control plane sees, and it is not on the
data path at all.

**C or C++.** The performance is not needed, and memory-safety bugs in
network-facing code that handles cryptographic material is exactly the failure
mode worth designing out.

**TypeScript on Node.** Workable for the server, unworkable for the client. A
privileged daemon that manages network interfaces should not carry a
JavaScript runtime and a dependency tree, and single-file distribution is
awkward.

**Different languages per component.** Rejected because the protocol types,
the configuration format and the API contract are shared. Two implementations
of one protocol is two chances to get it subtly different, and the difference
would show up as a connectivity bug rather than a compile error.

## Consequences

**Good.**

- One language across every backend component; shared packages are genuinely
  shared rather than reimplemented per component.
- `GOOS`/`GOARCH` cross-compilation gives seven platform targets from one CI
  job.
- Static binaries with no runtime dependency.
- `net/http`, `log/slog`, `database/sql` and `crypto` cover most of what this
  project needs without third-party code — which is why Headnet has three
  direct dependencies rather than thirty.
- Straightforward concurrency for a component maintaining many long-lived
  connections.

**Bad.**

- Garbage collection makes worst-case latency less predictable than Rust. Not
  a concern for a control plane, and irrelevant to the data plane.
- A less expressive type system than Rust: some invariants have to be enforced
  by tests rather than by the compiler.
- Larger binaries than equivalent C.

**Constraints this imposes.**

- `CGO_ENABLED=0` everywhere, so cross-compilation keeps working. That rules
  out cgo-based libraries, which is why the driver choice in
  [ADR-0005](ADR-0005-database-sqlite-postgres.md) matters more than it
  otherwise would.
- The race detector requires cgo, so it runs in a dedicated CI job rather than
  alongside the ordinary test matrix.
- Go is not the natural UI language on any platform, which the desktop and
  mobile decisions ([ADR-0006](ADR-0006-desktop-wails.md),
  [ADR-0007](ADR-0007-mobile-flutter.md)) have to account for.
