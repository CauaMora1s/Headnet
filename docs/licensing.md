# Licensing

Headnet uses two licences, split along one boundary.

| Path | Licence | File |
| --- | --- | --- |
| `packages/**` | **Apache-2.0** | [`packages/LICENSE`](../packages/LICENSE) |
| Everything else | **AGPL-3.0-or-later** | [`LICENSE`](../LICENSE) |

The reasoning, and the alternatives that were rejected, are in
[ADR-0009](architecture/decisions/ADR-0009-licensing.md).

> **This is the decision most worth revisiting early.** It is cheap to change
> now, while there are no outside contributors, and expensive later, when
> relicensing would need every contributor's agreement. If the AGPL is a
> problem for you, please open a discussion.

---

## What this means for you

### Running Headnet

**No obligations at all.** Deploy it, use it, run it for your company, run it
for your friends. AGPL obligations attach to *modifying and offering software
as a service*, not to using it.

### Modifying it for internal use

Still nothing. You may modify Headnet and run your modified version privately.
The AGPL is triggered by offering it to *others* over a network, not by
running it.

### Offering a modified Headnet as a service

This is where the AGPL bites. If you run a modified version and let other
people use it over a network, you must offer those users the source of your
modifications under the AGPL.

This is deliberate. It is what stops a large provider from taking Headnet,
improving it privately, and running it as a closed service in competition with
the project that built it — while contributing nothing back.

### Writing a client, integration or tool

Use `packages/` under **Apache-2.0**. That layer holds the protocol
definitions, API types and configuration loader — everything a third party
needs to interoperate — and it carries Apache-2.0's explicit patent grant.

You can build a proprietary Headnet client, a monitoring integration, or a
Terraform provider on top of it without any copyleft obligation.

**If you cannot write a client without reading `internal/`, the boundary is in
the wrong place.** That is a bug worth reporting.

---

## The boundary, in practice

Files under `packages/` carry an SPDX header:

```go
// SPDX-License-Identifier: Apache-2.0
// Copyright The Headnet Authors
```

Two rules keep the boundary sound:

1. **`packages/` must not import from `internal/`.** The Go toolchain enforces
   this for anyone outside the module; inside it, review does.
2. **`packages/` must not take a copyleft dependency.** CI's dependency review
   blocks GPL and LGPL additions.

The direction of dependency is one-way: `internal/` may use `packages/`, never
the reverse.

---

## Contributing

There is **no CLA**. Contributions are accepted under the licence of the
directory they land in — Apache-2.0 for `packages/`, AGPL-3.0-or-later for
everything else.

By opening a pull request you confirm you have the right to contribute the
code under those terms.

---

## Dependencies

Every dependency is under a permissive licence compatible with both halves.

| | Licence |
| --- | --- |
| `modernc.org/sqlite` | BSD-3-Clause |
| `github.com/jackc/pgx/v5` | MIT |
| `github.com/goccy/go-yaml` | MIT |

The Go standard library is BSD-3-Clause. Frontend dependencies are build-time
only and do not ship in the server binary.

CI blocks a pull request that introduces a GPL, LGPL or AGPL dependency —
which would conflict with the Apache-2.0 licensing of `packages/`.

---

## Third-party notices

The [Contributor Covenant](https://www.contributor-covenant.org/) 2.1, adopted
as [`CODE_OF_CONDUCT.md`](../CODE_OF_CONDUCT.md), is licensed CC BY 4.0.

Headnet is an independent project. It is not affiliated with, endorsed by, or
derived from the source of Tailscale, Headscale, NetBird, Nebula, Netmaker or
WireGuard. Architectural influence is acknowledged in the
[ADRs](architecture/decisions/); no code was copied from any of them.

"WireGuard" is a registered trademark of Jason A. Donenfeld.

---

## Questions

**Can I use Headnet at my company?** Yes, without obligation.

**Can I modify it for internal use?** Yes, without obligation.

**Can I offer it as a hosted service?** Yes, if you publish your modifications
under the AGPL.

**Can I build a proprietary client?** Yes. Build against `packages/`, which is
Apache-2.0.

**Can I fork it?** Yes, under the same licences.

**Will you dual-license it commercially?** There is no such offering today.
Single-copyright ownership has been preserved, so it remains possible.

**My company forbids AGPL software entirely.** This is the strongest argument
against the current choice, and it is recorded as such in
[ADR-0009](architecture/decisions/ADR-0009-licensing.md). Please open a
discussion — it is early enough that this can change.
