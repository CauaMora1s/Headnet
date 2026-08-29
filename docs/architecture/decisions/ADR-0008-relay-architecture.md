# ADR-0008: The relay forwards ciphertext and holds no keys

**Status:** Accepted
**Date:** 2026-08-29
**Implements:** [Phase 5](../../ROADMAP.md#phase-5--relay)

## Context

Direct peer-to-peer connections are the goal, and NAT traversal
([Phase 4](../../ROADMAP.md#phase-4--nat-traversal)) will achieve them in most
cases. It will not achieve them in all: two peers behind symmetric NAT, or
behind a firewall that blocks outbound UDP entirely, cannot reach each other
no matter how clever the hole punching.

Without a fallback, those users simply cannot connect. With the wrong
fallback, every user's traffic passes through a machine that can read it.

## Decision

A separate relay component that **forwards WireGuard packets it cannot
decrypt**.

- The relay holds **no WireGuard keys** and has no mechanism to obtain them.
- Peers relay the same encrypted packets they would have sent directly; the
  relay changes the path, not the payload.
- Relaying is **fallback only**, attempted after direct connection fails, and
  peers keep probing for a direct path afterwards.
- The client **tells the user** when it is relaying.
- Access requires a short-lived, audience-restricted token issued by the
  control plane.

## Alternatives

**No relay.** Simplest and most secure, and it abandons the users who need it
most — often exactly those on restrictive corporate or mobile networks.

**A relay that terminates and re-encrypts.** Rejected absolutely. It would put
plaintext on a machine that is, by construction, the most exposed component in
the system, and it would make "the relay operator can read your traffic" true.
That would undermine the property the whole architecture is built to protect.

**Relay everything, always.** Simpler to reason about and much easier to
operate — this is roughly how a traditional hub-and-spoke VPN works. Rejected
on latency, bandwidth cost and the operator's exposure. Direct connections are
faster, cheaper and involve fewer parties.

**Use the control plane as the relay.** Rejected. The control plane and the
relay have genuinely different profiles: the control plane holds the identity
database and should be as unexposed as possible, while a relay is
high-bandwidth, high-exposure and ideally deployed in several locations. They
scale differently and fail differently, and merging them would drag the
identity database into the most attacked component.

**TURN.** The standard answer, and a reasonable one. Rejected as a
starting point because TURN carries allocation semantics and a permission
model Headnet does not need, and because a purpose-built forwarder that only
has to move WireGuard packets between authenticated peers is a much smaller
thing to secure. Revisited if interoperability with existing infrastructure
becomes valuable.

## Consequences

**Good.**

- A compromised relay yields ciphertext, not content.
- Users behind hostile networks can still connect.
- Relays can be deployed separately, in multiple locations, without exposing
  the identity database.
- Operators can run their own relays, or none.

**Bad.**

- Higher latency and bandwidth cost when relaying.
- Another component to deploy, monitor and secure.
- **A relay operator sees traffic metadata** — which peers communicate, when,
  how much, how often. This is inherent to relaying and is documented in the
  [threat model](../../security/threat-model.md#45-compromised-relay) rather
  than glossed over. Headnet does not claim traffic-analysis resistance.
- Relay capacity has to be planned; it is a shared resource.

**Constraints this imposes.**

- The relay must never be given a WireGuard private key, and a test will
  assert that relayed data is not decryptable with anything the relay holds.
- Relay tokens are short-lived and audience-restricted, so a leaked token is
  not durable access. An unauthenticated relay is an open proxy.
- Per-client rate limits, bandwidth limits, connection caps and idle timeouts
  are requirements, not enhancements: the relay is the most exposed component
  in the system.
- Falling back to a relay must be **visible**. A user is entitled to know that
  their traffic is taking a longer path through another machine.
