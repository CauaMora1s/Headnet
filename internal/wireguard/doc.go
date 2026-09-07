// Package wireguard holds a device's WireGuard key material and, in time, its
// network interface.
//
// Headnet configures WireGuard; it does not reimplement any part of it. All
// traffic cryptography is WireGuard's, unmodified. See
// docs/architecture/decisions/ADR-0002-use-wireguard.md.
//
// # What is implemented
//
// Key generation and storage, which is Phase 2 items 1 and 2 of
// docs/ROADMAP.md#phase-2--client-mvp:
//
//   - Curve25519 key pairs from the platform CSPRNG, clamped exactly as
//     WireGuard clamps them, and checked against the RFC 7748 test vector.
//   - A PrivateKey type that cannot be rendered, marshalled or logged. This is
//     what makes the promise in docs/security/key-management.md structural
//     rather than a matter of everybody remembering.
//   - A Store that writes the key so only the account running the daemon can
//     read it — 0600 in a 0700 directory on Unix, an explicit protected DACL
//     on Windows — and, crucially, *verifies* that on every read rather than
//     only setting it on write.
//
// # What is not
//
// **Nothing here brings up a network interface, and nothing carries traffic.**
// Interface lifecycle, address configuration and peer reconciliation are the
// rest of Phase 2 and Phase 3. A device with a key on disk is not connected to
// anything, and no function in this package will tell you otherwise.
//
// AllowedIPs, when it arrives, is the security-critical surface: WireGuard
// uses it for cryptokey routing, so a mistake there is a routing
// vulnerability rather than a cosmetic bug.
package wireguard
