// Package devices is not implemented yet.
//
// It is planned for Phase 1 of the roadmap; see
// docs/ROADMAP.md#phase-1--server-mvp.
//
// This file exists so the intended shape of the control plane is visible in
// the source tree, and so that nothing here can be mistaken for working code.
// There is no implementation behind it.
//
// Device lifecycle: enrolment, inventory, revocation.
//
// A device record holds a WireGuard *public* key and never a private one. The
// private half is generated on the device and never transmitted, which is
// what makes a control-plane compromise survivable.
//
// Revocation must disconnect rather than merely delist. A device removed from
// a list whose peers still hold its configuration is still on the network.
//
// See docs/security/key-management.md.
package devices
