# Protocol versioning

Servers and clients are upgraded independently. A fleet of laptops does not
restart when an administrator upgrades the control plane, and a subnet router
in a cupboard may lag by months.

This document describes the contract that makes that survivable. It is
**implemented and tested** in `packages/protocol`.

---

## The rules

1. **One integer.** No minor or patch protocol versions. It increments only
   for a breaking change to the wire contract.
2. **Each side advertises a range**: the newest version it speaks, and the
   oldest it still accepts. Two peers are compatible when their ranges
   overlap.
3. **Optional behaviour is a named capability**, never a version comparison.
   Adding a capability is always backwards compatible.
4. **Unknown capabilities are ignored**, not rejected. An old peer talking to
   a new one degrades gracefully rather than failing.

Rule 3 is the one that does the work. Without it, every additive feature would
need a version bump, every version bump would strand older clients, and the
supported range would have to grow without limit.

---

## Negotiation

```
Server speaks 1..3
Client speaks 1..2
              ↓
Agreed: version 2, capabilities = server ∩ client
```

The agreed version is the **highest both accept**, so a new client uses new
behaviour against a new server and falls back cleanly against an old one.

The agreed capabilities are the **intersection**. A feature may only be used
when both ends independently claim to implement it.

### The endpoint

```http
POST /api/v1/protocol/negotiate
Content-Type: application/json

{
  "protocol_version": 1,
  "min_protocol_version": 1,
  "capabilities": ["protocol.negotiation"],
  "client_version": "0.1.0"
}
```

```json
{
  "protocol_version": 1,
  "capabilities": ["protocol.negotiation"],
  "server_version": "0.1.0"
}
```

`min_protocol_version` may be omitted, in which case the client is assumed to
speak exactly one version. `client_version` is recorded for diagnostics and
fleet visibility only; it never affects the outcome.

### When they do not overlap

```json
{
  "error": {
    "code": "unsupported_protocol_version",
    "message": "incompatible protocol version: the peer is too old (local supports 2..3, peer supports 1..1); upgrade the peer",
    "details": { "server_protocol_version": "3" }
  }
}
```

`426 Upgrade Required`, and the message says **which side** must be upgraded.
That specificity is the point: an operator reading this should not have to
work out whether to upgrade the server or the client.

Doing this up front turns a version mismatch into one clear error at the start
of a session, rather than a confusing failure several requests later when some
unrelated call returns something unexpected.

---

## Capabilities

Lower-case, dot-separated, namespaced by subsystem so they read sensibly in a
log:

```
protocol.negotiation
relay.fallback
dns.magic
routes.subnet
```

**A capability is added only once the behaviour behind it works end to end.**
Advertising one a peer cannot actually use is a protocol bug, not a
placeholder — the whole mechanism depends on an advertisement being a
promise.

### Currently advertised

| | |
| --- | --- |
| `protocol.negotiation` | Explicit version and capability handshake |

That is the complete list. It will grow with the roadmap.

---

## When to increment the version

**Increment** for a change that an older peer cannot handle:

- Removing or renaming a required field
- Changing the meaning of an existing field
- Changing the framing or transport of an existing message

**Do not increment** for:

- A new endpoint
- A new optional field
- A new capability
- Anything additive

Almost everything is additive. If you find yourself wanting to increment,
check first whether the change can be expressed as a capability instead —
usually it can, and usually that is the better design anyway.

Every increment requires a note in [upgrade.md](../upgrade.md).

---

## The current version

```go
const Version    = 1   // the newest this build speaks
const MinVersion = 1   // the oldest this build accepts
```

Raising `MinVersion` **drops support for older clients** and is a breaking
change for operators, so it may only happen in a major release, and only after
the version being dropped has been deprecated for a full release cycle.

---

## API version versus protocol version

Two different things, often confused:

| | Governs | Changes |
| --- | --- | --- |
| **API version** (`/api/v1`) | The HTTP surface | Only for a breaking HTTP change |
| **Protocol version** | The client/server contract as a whole | For a breaking behavioural change |

A protocol change does not necessarily change the API path. Adding a required
field to an existing request would be a protocol change while `/api/v1`
remains `/api/v1` — because the *shape* of the exchange changed, not the
addressing.

---

## Checking compatibility

```bash
headnet diagnostics -server https://vpn.example.com
```

```
✓  Protocol compatibility    agreed on protocol v1 (this build speaks 1–1, the server speaks 1–1)
```

Or directly:

```bash
curl -s https://vpn.example.com/api/v1/version | jq '{protocol_version, min_protocol_version, capabilities}'
```

---

## Why this shape

The alternative most projects reach for is semantic versioning of the
protocol, with clients checking `major.minor` compatibility. It was rejected
for two reasons:

- **A minor version is a bundle.** It says "this peer has all the features
  from 1.0 to 1.7", which forces features to ship in a strict order and
  strands a client that is missing only one of them.
- **Capabilities are honest about partial support.** A client that implements
  relay fallback but not Magic DNS can say exactly that. Under semantic
  versioning it would have to claim a version it does not fully implement, or
  a version older than it deserves.

The cost is a small amount of bookkeeping — a capability name per optional
feature. In exchange, most changes need no version bump at all.
