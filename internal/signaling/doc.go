// Package signaling is not implemented yet.
//
// It is planned for Phase 4 of the roadmap; see
// docs/ROADMAP.md#phase-4--nat-traversal.
//
// This file exists so the intended shape of the control plane is visible in
// the source tree, and so that nothing here can be mistaken for working code.
// There is no implementation behind it.
//
// NAT-traversal coordination: candidate exchange between peers.
//
// Endpoint candidates are private information — they reveal a user's rough
// location and ISP — so they are shared only with peers already authorised to
// connect.
//
// A discovered endpoint is a hint, never a fact: a malicious or spoofed STUN
// server can lie about a reflexive address, and only a completed WireGuard
// handshake confirms a path.
//
// See docs/networking.md.
package signaling
