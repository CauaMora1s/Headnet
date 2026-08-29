// Package network is not implemented yet.
//
// It is planned for Phase 1 of the roadmap; see
// docs/ROADMAP.md#phase-1--server-mvp.
//
// This file exists so the intended shape of the control plane is visible in
// the source tree, and so that nothing here can be mistaken for working code.
// There is no implementation behind it.
//
// IP address management for the overlay network.
//
// Addresses are allocated from the pools configured in internal/config, whose
// validation rules are already implemented and tested.
//
// Allocation will be a database-level uniqueness guarantee rather than
// application logic, because concurrent enrolment is exactly the situation
// where application-level checks race and hand two devices the same address.
//
// See docs/networking.md.
package network
