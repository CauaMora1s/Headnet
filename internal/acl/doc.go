// Package acl is not implemented yet.
//
// It is planned for Phase 6 of the roadmap; see
// docs/ROADMAP.md#phase-6--authorization.
//
// This file exists so the intended shape of the control plane is visible in
// the source tree, and so that nothing here can be mistaken for working code.
// There is no implementation behind it.
//
// The authorization policy engine.
//
// Default deny: an empty policy means no traffic, not all traffic.
//
// Enforcement is in peer distribution, not in the client. A device that may
// not reach another is never sent that peer's configuration, so the
// restriction holds even against a malicious client — which client-side
// filtering could never guarantee.
//
// See docs/ROADMAP.md#phase-6--authorization.
package acl
