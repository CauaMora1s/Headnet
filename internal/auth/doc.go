// Package auth is not implemented yet.
//
// It is planned for Phase 1 of the roadmap; see
// docs/ROADMAP.md#phase-1--server-mvp.
//
// This file exists so the intended shape of the control plane is visible in
// the source tree, and so that nothing here can be mistaken for working code.
// There is no implementation behind it.
//
// Authentication providers.
//
// The design is one abstraction with two implementations rather than one per
// vendor: Google, GitHub Enterprise, Microsoft Entra, Okta, Keycloak and Dex
// are all OIDC issuers, and writing a bespoke integration for each would
// multiply the security-critical surface for no functional gain.
//
//	AuthProvider
//	├── Local   email + password, Argon2id
//	└── OIDC    any compliant issuer
//
// Sessions will be server-side, not stateless tokens: when a device is
// stolen, an administrator must be able to revoke access immediately, and a
// self-contained token cannot be revoked before it expires.
//
// See docs/authentication.md and
// docs/architecture/decisions/ADR-0004-authentication-oidc.md.
package auth
