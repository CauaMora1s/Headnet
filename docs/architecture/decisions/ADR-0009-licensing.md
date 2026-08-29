# ADR-0009: AGPL-3.0 with an Apache-2.0 interoperability layer

**Status:** Accepted
**Date:** 2026-08-29

> **This decision is the one most worth revisiting early.** It is cheap to
> change now, while there are no outside contributors, and expensive later,
> when relicensing would require every contributor's agreement. If you are
> evaluating Headnet and this is a problem, please open a discussion.

## Context

Headnet is open source, self-hosted, and might one day have a hosted or
commercial offering alongside it. Those goals pull the licence choice in
different directions:

- **Adoption** favours a permissive licence. People deploy and contribute to
  things they do not have to think about legally.
- **Sustainability** favours copyleft. A permissive licence lets a large cloud
  provider run Headnet as a closed service, contributing nothing back, while
  competing with the project that built it.
- **Interoperability** favours permissive licensing for the *protocol*.
  Someone writing an alternative client, or embedding Headnet awareness into
  their own tool, should not have to take on a copyleft obligation to do it.
  A closed ecosystem is a worse outcome than a permissive one.

## Decision

**AGPL-3.0-or-later** for the repository, with **Apache-2.0** for everything
under `packages/`.

| Path | Licence | Why |
| --- | --- | --- |
| `packages/**` | Apache-2.0 | The protocol, API types and configuration loader a third party needs to interoperate |
| Everything else | AGPL-3.0-or-later | The control plane, client, relay, CLI and web UI |

The `packages/` files carry SPDX headers, so the boundary is visible in the
source rather than only in a document.

No CLA. Contributions are accepted under the licence of the directory they
land in.

## Alternatives

**MIT or BSD-3.** Maximum adoption, and what Tailscale, Headscale and NetBird
use. Rejected because it permits a closed hosted fork with no obligation to
contribute back — the specific outcome that makes sustaining an open project
hard. It is a defensible choice and reasonable people pick it; this project
weighs sustainability higher.

**Apache-2.0 throughout.** Same trade-off as MIT, plus an explicit patent
grant. Genuinely attractive, and the strongest alternative. Rejected for the
same reason, and its patent grant is preserved for the part where it matters
most — the interoperability surface.

**GPL-3.0.** Copyleft, but its trigger is *distribution*. Running modified
server software as a network service is not distribution, so it does not
address the case this project cares about. AGPL exists precisely to close that
gap.

**AGPL-3.0 for absolutely everything.** Simpler, one licence, no boundary to
police. Rejected because it would make the protocol package itself copyleft,
discouraging third-party clients and integrations. A protocol that is
awkward to implement against is a protocol nobody implements against.

**A source-available licence (BUSL, SSPL, Elastic).** Rejected. Neither is
OSI-approved and neither is open source. The project claims to be open source
and must actually be.

## Consequences

**Good.**

- Self-hosters are unaffected. Running Headnet imposes no obligation
  whatsoever; AGPL obligations attach to *modifying and offering it as a
  service*.
- A hosted competitor must publish its modifications.
- Third parties can write clients, integrations and tooling against
  `packages/` under Apache-2.0, patent grant included.
- Single-copyright ownership stays possible, which keeps a future dual-licence
  option open.

**Bad.**

- **AGPL deters some corporate adoption.** Many companies have blanket
  policies against it, even for software they only deploy internally. This is
  a real cost, and it is the strongest argument for reconsidering.
- Two licences in one repository is more complexity than one, and requires the
  boundary to be maintained.
- Relicensing later needs every contributor's agreement, absent a CLA.
- Some contributors avoid AGPL projects on principle.

**Constraints this imposes.**

- **`packages/` may not import from `internal/`**, or from any copyleft
  dependency. CI's dependency review enforces the licence half of this; the Go
  toolchain enforces the `internal/` half.
- New files under `packages/` carry an Apache-2.0 SPDX header.
- Anything genuinely needed to interoperate belongs in `packages/`. If a third
  party cannot write a client without reading `internal/`, the boundary is in
  the wrong place and should move.

## Revisiting this

Reconsider if:

- Corporate adoption is measurably blocked by the AGPL.
- A contributor of substance declines over it.
- The commercial rationale disappears, in which case Apache-2.0 throughout
  becomes the obvious answer.

See [docs/licensing.md](../../licensing.md) for the practical guidance.
