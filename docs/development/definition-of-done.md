# Definition of Done

A feature is not finished when it works. It is finished when someone else can
run it, understand it, operate it, and trust it.

This is the checklist in the pull request template, with what each item
actually means.

---

## The checklist

### Implementation complete

No half-built paths behind a flag. No "this branch is never taken yet". If a
piece is deferred, it is deferred *visibly* — a `501`, a non-zero exit, an
explicit note — not left as a code path that quietly does nothing.

### Unit tests, including the failure modes

The happy path is the easy half. What happens with invalid input, a missing
dependency, a cancelled context, a concurrent write?

See [testing.md](testing.md).

### Integration tests where the change crosses a boundary

If the change touches the database, the HTTP surface, or two components
together, test it against the real thing. A mocked database tests the mock.

### Errors handled, with messages that tell a user what to do next

Error messages are read by an operator at three in the morning.

```
Not this:   invalid config
This:       server.base_url: "ftp://x" scheme is not supported (expected http or https)
```

For a failure a user can act on, say what to do:

```
server.base_url: plain HTTP is refused in production because enrolment
credentials and session cookies would travel in the clear; use https, or set
server.allow_insecure_http when TLS is terminated by a proxy in front of this
server
```

### Security implications considered

Write them down in the pull request, even if the answer is "none".

- What new input does this accept, and what validates it?
- What new authorisation decision does it make?
- Could it leak key material, a token or a password into a log or a response?
- Does it change what an unauthenticated caller can reach?
- Does it add a new default, and is that default safe?

If it touches identity, keys, policy or the network edge, re-read the
[threat model](../security/threat-model.md) and update it if the change adds
attack surface.

### Logging, with no secrets in it

Enough to diagnose a failure from logs alone. Structured, with the request ID
travelling in the context.

Never: private keys, passwords, session tokens, setup keys, OIDC client
secrets, database DSNs, or query strings.

### Metrics where the feature has an operational signal

Not everything needs a metric. Ask what an operator would want to alert on.
(Prometheus support is Phase 12; until then this means making sure the signal
*exists* in the logs.)

### Documentation written alongside the code

Not afterwards. A feature merged without documentation is a feature nobody can
use, and documentation written a week later describes what you remember rather
than what you built.

### API documentation updated

New or changed endpoints go into
[`docs/api/openapi.yaml`](../api/openapi.yaml) in the same pull request. The
specification is the contract; a stale one is worse than none.

### Configuration documented

A new setting means updating all three:

- [`docs/configuration.md`](../configuration.md) — what it does, what breaks
  if it is wrong
- [`headnet.example.yaml`](../../headnet.example.yaml)
- [`.env.example`](../../.env.example)

### CLI or UI support if the feature is user-facing

A server feature nobody can reach is not a feature. Either wire it up, or say
explicitly in the pull request which follow-up will.

### Migration included if the schema changed

Versioned, embedded, for **both** dialects — SQLite and PostgreSQL. There is a
test that fails if one gains a migration the other lacks.

Never edit an applied migration; the checksum ledger will refuse it, and for a
good reason. If a migration cannot be safely reversed, say so in the migration
file and in [`docs/upgrade.md`](../upgrade.md).

### CI passes

All of it, including the race detector and the security workflow. Not "it is
just a flake" — investigate it.

### No untracked `TODO`

A `TODO` is fine if an issue tracks it and the comment says which. A bare
`TODO` is a promise nobody is holding.

---

## The honesty requirements

These are specific to this project and non-negotiable.

### Nothing may look implemented that is not

- API endpoints return `501` with the roadmap phase, or do not exist. Never an
  empty success.
- CLI commands exit non-zero and say what is missing.
- The UI says a thing is unbuilt rather than showing an empty table of it.
- Diagnostics report unbuilt subsystems as *skipped*, never as passing.

There are tests asserting this. They are not obstacles to route around.

### No invented data anywhere

No placeholder device lists, no sample users, no simulated connection states.
If a screen has nothing real to show, it says so.

### The reason

A VPN client that reports "connected" when nothing is running can lead someone
to send sensitive traffic over a network they believe is protected. That makes
this a security property, not a matter of taste — which is why it is in the
[threat model](../security/threat-model.md#6-security-properties-headnet-intends-to-hold)
alongside the cryptographic ones.

---

## Extra bar for security-sensitive changes

Anything touching authentication, authorisation, key handling, enrolment or
the network edge also needs:

- [ ] The threat model updated if the attack surface changed
- [ ] A test for each security property the change is responsible for, with a
      comment explaining what it protects
- [ ] Explicit reasoning for any new default, and a bias towards the
      restrictive option
- [ ] No new dependency on the request path without justification
- [ ] Review by someone other than the author

---

## What this is not

Not a way to block contributions. Most items are quick, and several are
usually not applicable.

The point is that the person who understands the change is the author, and
they are the only one positioned to do these things cheaply. Anything left
undone here becomes someone else's expensive problem later — usually during an
incident.
