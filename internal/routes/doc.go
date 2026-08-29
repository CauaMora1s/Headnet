// Package routes is not implemented yet.
//
// It is planned for Phase 8 of the roadmap; see
// docs/ROADMAP.md#phase-8--routes.
//
// This file exists so the intended shape of the control plane is visible in
// the source tree, and so that nothing here can be mistaken for working code.
// There is no implementation behind it.
//
// Subnet routing: making a whole network reachable through one device.
//
// Advertising a route is not the same as having it approved. An administrator
// must approve every route before it is distributed, because without that
// separation one compromised device could claim 0.0.0.0/0 and become a man in
// the middle for the entire network.
//
// See docs/networking.md.
package routes
