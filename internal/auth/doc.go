// Package auth is the identity domain: accounts, passwords, and the
// authentication providers behind them.
//
// The design decision that shapes this package is that there are two provider
// implementations, not one per vendor. Google, GitHub Enterprise, Microsoft
// Entra, Okta, Keycloak and Dex are all OpenID Connect issuers, so they are
// configuration rather than code:
//
//	AuthProvider
//	├── Local   email + password, Argon2id      implemented
//	└── OIDC    any compliant issuer            Phase 11
//
// Writing a bespoke integration for each would multiply the most
// security-critical surface in the project for no functional gain. See
// docs/architecture/decisions/ADR-0004-authentication-oidc.md.
//
// Three properties this package is responsible for:
//
//   - A password is never stored, logged or returned. Only an Argon2id hash in
//     PHC string format, which carries its own cost parameters so they can be
//     raised later without invalidating existing passwords.
//   - A user's password hash is unexported, so it cannot reach a JSON encoder,
//     a template or a struct-printing log call by accident.
//   - Looking up a non-existent account must not be distinguishable from a
//     wrong password. DummyHash exists so the login path can do equal work in
//     both cases; without it the endpoint tells an attacker which addresses
//     are registered.
//
// Sessions and the login flow live alongside this in session.go.
package auth
