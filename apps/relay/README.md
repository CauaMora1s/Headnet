# Relay server

**Status: not implemented.** Planned for
[Phase 5](../../docs/ROADMAP.md#phase-5--relay).

This directory is a placeholder. It contains no code, deliberately — an empty
directory that looks like a component is worse than an honest note.

## What will go here

Forwards encrypted traffic between peers that cannot reach each other
directly — two symmetric NATs, or a network that blocks outbound UDP.

**The relay holds no WireGuard keys and cannot decrypt what passes through
it.** It forwards ciphertext. A compromised relay sees which peers communicate
and when; it does not see what they say.

It is a separate binary from the control plane because the two have genuinely
different profiles: the control plane holds the identity database and should
be as unexposed as possible, while a relay is high-bandwidth, high-exposure
and ideally deployed in several locations.

See [ADR-0008](../../docs/architecture/decisions/ADR-0008-relay-architecture.md).
