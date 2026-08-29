# ADR-0007: Flutter for the mobile applications

**Status:** Accepted
**Date:** 2026-08-29
**Implements:** [Phase 10](../../ROADMAP.md#phase-10--mobile)

## Context

Headnet needs Android and iOS clients. Phones are where a mesh VPN is most
useful and hardest to build: mobile networks are aggressively NATed, operating
systems kill background processes, and both platforms require VPN traffic to
go through their own frameworks.

## Decision

**Flutter** for the user interface on both platforms, with **platform-native
VPN integration** underneath:

- Android: `VpnService`
- iOS: `NetworkExtension` / `NEPacketTunnelProvider`

The desktop client daemon is **not** ported to mobile. What is reused is the
API contract, the authentication protocol, the data model, the configuration
format and the network concepts — not the process model.

## Alternatives

**React Native.** A larger ecosystem and more available developers. Rejected
on the same grounds as the desktop decision: heavier runtime, and the JS
bridge is an awkward place to sit next to a packet tunnel.

**Fully native, twice** (Kotlin and Swift). The best possible result and
roughly double the work, on the platform where this project has the least
capacity. Reconsidered if the Flutter layer proves to be the constraint.

**Running the Go daemon on mobile via gomobile.** Seriously considered, and
rejected. Both platforms require the VPN to be implemented through their own
extension APIs with their own lifecycle and memory limits — iOS gives a packet
tunnel extension a very small budget. Forcing a desktop daemon architecture
into that shape would fight the platform continuously. `wireguard-go` does
build for mobile and may well be used *inside* the platform extension; that is
different from running the daemon.

**A mobile web app.** Not possible. Neither platform lets a web application
create a VPN tunnel.

## Consequences

**Good.**

- One UI codebase for both platforms.
- Good performance and a native feel.
- Mature tooling and a large ecosystem.
- Platform VPN APIs are used as intended, which is the only way background
  behaviour and battery use are acceptable.

**Bad.**

- A third language (Dart) alongside Go and TypeScript.
- Platform channels are needed between Flutter and the native VPN extension,
  which is the fiddly part of this design.
- App Store and Play Store review, signing and distribution overhead.
- iOS `NetworkExtension` requires a paid developer account and a specific
  entitlement, which is a real barrier for a community project and may
  constrain how iOS builds are distributed.

**Constraints this imposes.**

- Keys live in the Android Keystore or the iOS Keychain, hardware-backed where
  available — never in application storage.
- QR enrolment codes must be short-lived, single-use and scoped, because a QR
  code is trivially photographed over someone's shoulder.
- **A VPN that quietly stops is a security failure, not an inconvenience.**
  Background restrictions must not be allowed to drop the tunnel silently; if
  the tunnel is down, the user has to know.
- The API contract must stay implementable in Dart, which means no
  Go-specific serialisation anywhere in `packages/api`.
