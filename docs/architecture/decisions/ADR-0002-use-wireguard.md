# ADR-0002: WireGuard as the data plane

**Status:** Accepted
**Date:** 2026-08-29

## Context

Headnet has to move encrypted traffic between devices. There are two ways to
get a secure transport: build one, or use one that already exists and has been
examined by people who do that for a living.

This is the decision with the largest security consequence in the project, so
the reasoning is written out rather than treating WireGuard as an obvious
default.

## Decision

WireGuard, unmodified, for all traffic between devices. Headnet implements no
cryptography of its own.

Headnet's job is everything *around* WireGuard: deciding which peers exist,
allocating addresses, distributing configuration, and enforcing policy.

## Alternatives

**Design a custom protocol.** Rejected quickly. Designing a secure transport
is a research undertaking, and the failure mode is silent — a subtly broken
protocol works perfectly until someone competent looks at it. The project
principle "never invent cryptography when an established standard exists"
exists primarily because of this decision.

**OpenVPN.** Mature and widely deployed, but a far larger codebase and attack
surface, slower, and unpleasant to configure programmatically.

**IPsec.** Standardised, and hardware-accelerated in places. Rejected for
complexity: IKEv2 configuration is precisely the kind of thing a user should
never have to see, and its NAT traversal is harder to get right.

**A QUIC-based transport.** Genuinely attractive — encryption is built in and
it behaves well through NAT. Rejected because it would mean designing the key
management and authorisation model ourselves, which is the part WireGuard has
already got right, and because kernel WireGuard outperforms any userspace
transport.

## Consequences

**Good.**

- A small, audited, partly formally-verified codebase handles all traffic
  cryptography.
- The Linux kernel implementation is very fast; `wireguard-go` covers
  platforms without one.
- Forward secrecy, replay protection and authenticated encryption come for
  free rather than being our problem.
- WireGuard is silent by design: an unauthenticated packet gets no response,
  so an endpoint is not discoverable by scanning.
- Users benefit from WireGuard's ongoing security review rather than ours.

**Bad.**

- WireGuard has no NAT traversal, so
  [Phase 4](../../ROADMAP.md#phase-4--nat-traversal) has to build endpoint
  discovery and hole punching around it.
- It has no relaying, hence [ADR-0008](ADR-0008-relay-architecture.md).
- Key rotation above the session level is our problem.
- `AllowedIPs` becomes a security-critical configuration surface. A mistake
  there is a routing vulnerability, not a cosmetic bug.
- Installation varies by platform: a kernel module, a userspace
  implementation, or a platform VPN API on mobile.

**Constraints this imposes.**

- Private keys are generated on the device, because that is how WireGuard is
  meant to be used and because it is what makes a control-plane compromise
  survivable. See [key management](../../security/key-management.md).
- Peer configuration is the enforcement point for authorisation, so policy has
  to be applied when computing peer sets — never client-side, which a
  malicious client would simply ignore.
