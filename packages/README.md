# packages/

Shared contracts, licensed **Apache-2.0** ([LICENSE](LICENSE)) rather than
AGPL-3.0 like the rest of the repository.

This is the surface a third party needs in order to interoperate with Headnet:
the protocol rules, the API types, and the configuration mechanics. Someone
writing an alternative client, a monitoring integration or a provisioning tool
should be able to build on this without taking on a copyleft obligation.

See [docs/licensing.md](../docs/licensing.md) and
[ADR-0009](../docs/architecture/decisions/ADR-0009-licensing.md).

## Contents

| | |
| --- | --- |
| [`protocol/`](protocol/) | Version and capability negotiation |
| [`api/`](api/) | HTTP data-transfer objects and the error contract |
| [`config/`](config/) | Layered configuration loading and secret redaction |
| [`shared/`](shared/) | Small dependency-free helpers: IDs, clock |

## Rules

Three, and they are what keep the licence boundary sound:

1. **Nothing here may import from `internal/`.** The dependency direction is
   one-way. The Go toolchain enforces this for anyone outside the module;
   inside it, review does.
2. **Nothing here may take a copyleft dependency.** It would contradict the
   Apache-2.0 licensing. CI's dependency review blocks GPL and LGPL
   additions.
3. **No business logic.** This layer describes the contract. Decisions about
   what the control plane *does* belong in `internal/`.

Every file carries an SPDX header:

```go
// SPDX-License-Identifier: Apache-2.0
// Copyright The Headnet Authors
```

## What belongs here

Ask: *could a third party write a working client without this?*

If the answer is no, it belongs here. If a third party cannot write a client
without reading `internal/`, the boundary is in the wrong place and something
should move — that is worth reporting as a bug.

When in doubt, put it in `internal/`. Moving something out to `packages/`
later is easy; taking it back is a breaking change for anyone who started
using it.
