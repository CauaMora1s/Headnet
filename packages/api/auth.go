// SPDX-License-Identifier: Apache-2.0
// Copyright The Headnet Authors

package api

import "time"

// Header and cookie names used by the authentication flow.
//
// They are part of the public contract: a non-browser client has to know where
// to put the CSRF token, and a browser client has to know which cookie to read
// it from.
const (
	// CSRFHeader is where a client echoes the CSRF token on any request that
	// changes state.
	CSRFHeader = "X-CSRF-Token"
	// SessionCookie holds the session token. It is HttpOnly; no client should
	// expect to read it.
	SessionCookie = "headnet_session"
	// CSRFCookie holds the CSRF token and is readable by script, because
	// echoing it back in CSRFHeader is the whole mechanism.
	CSRFCookie = "headnet_csrf"
)

// LoginRequest is the body of POST /api/v1/auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// BootstrapRequest is the body of POST /api/v1/auth/bootstrap, which creates
// the first administrator on a fresh installation.
type BootstrapRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name,omitempty"`
}

// User is an account as the API represents it.
//
// There is deliberately no password field of any kind — not the hash, not a
// placeholder. A DTO that has somewhere to put a password hash is a DTO that
// will eventually carry one.
type User struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	DisplayName string     `json:"display_name,omitempty"`
	Role        string     `json:"role"`
	Provider    string     `json:"provider"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

// Session describes the caller's current session. It is returned by login,
// by bootstrap, and by GET /api/v1/auth/session.
type Session struct {
	// User is the signed-in account.
	User User `json:"user"`
	// CSRFToken must be echoed in CSRFHeader on every state-changing request.
	// It is also set as CSRFCookie, so a browser client may read it from
	// there instead.
	CSRFToken string `json:"csrf_token"`
	// ExpiresAt is when the session stops being valid regardless of use.
	ExpiresAt time.Time `json:"expires_at"`
}

// AuthStatus is returned by GET /api/v1/auth/status. It is unauthenticated,
// so that a client can tell which sign-in flow to present before it has any
// credentials — and it discloses nothing beyond that.
type AuthStatus struct {
	// BootstrapRequired reports that no account exists yet and the
	// installation is waiting to be claimed.
	BootstrapRequired bool `json:"bootstrap_required"`
	// Providers lists the enabled authentication backends.
	Providers []string `json:"providers"`
}
