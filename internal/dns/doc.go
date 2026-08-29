// Package dns is not implemented yet.
//
// It is planned for Phase 7 of the roadmap; see
// docs/ROADMAP.md#phase-7--dns.
//
// This file exists so the intended shape of the control plane is visible in
// the source tree, and so that nothing here can be mistaken for working code.
// There is no implementation behind it.
//
// Internal DNS, so devices are reachable by name rather than by address.
//
// Names are assigned by the server, not claimed by devices; otherwise one
// device could hijack another's name simply by setting its hostname.
//
// An established DNS server will be evaluated before writing one. Building a
// DNS server is a large project with a bad failure mode.
package dns
