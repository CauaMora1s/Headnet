// Package relay is not implemented yet.
//
// It is planned for Phase 5 of the roadmap; see
// docs/ROADMAP.md#phase-5--relay.
//
// This file exists so the intended shape of the control plane is visible in
// the source tree, and so that nothing here can be mistaken for working code.
// There is no implementation behind it.
//
// Control-plane side of relay coordination: which relays exist, and issuing
// the short-lived tokens clients present to them.
//
// The relay itself is a separate binary (apps/relay). It forwards WireGuard
// ciphertext and holds no keys that could decrypt it.
//
// See docs/architecture/decisions/ADR-0008-relay-architecture.md.
package relay
