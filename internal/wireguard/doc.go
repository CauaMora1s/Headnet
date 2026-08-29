// Package wireguard is not implemented yet.
//
// It is planned for Phase 2 of the roadmap; see
// docs/ROADMAP.md#phase-2--client-mvp.
//
// This file exists so the intended shape of the control plane is visible in
// the source tree, and so that nothing here can be mistaken for working code.
// There is no implementation behind it.
//
// WireGuard interface management, per platform.
//
// Headnet configures WireGuard; it does not reimplement any part of it. All
// traffic cryptography is WireGuard's, unmodified.
//
// AllowedIPs is a security-critical surface here: WireGuard uses it for
// cryptokey routing, so a mistake is a routing vulnerability rather than a
// cosmetic bug.
//
// See docs/architecture/decisions/ADR-0002-use-wireguard.md.
package wireguard
