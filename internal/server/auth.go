package server

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/CauaMora1s/Headnet/internal/auth"
	"github.com/CauaMora1s/Headnet/internal/httpapi"
	"github.com/CauaMora1s/Headnet/internal/storage"
	"github.com/CauaMora1s/Headnet/packages/api"
)

// invalidCredentials is the single message returned for every failed sign-in.
//
// Whether the address is unknown, the password is wrong, or the account has no
// local password, the caller is told exactly the same thing. Any difference
// turns this endpoint into a way to discover which addresses are registered.
const invalidCredentials = "the email address or password is incorrect"

// handleAuthStatus reports whether the installation has been claimed and which
// providers are enabled.
//
// It is unauthenticated because a client needs it before it has any
// credentials, in order to know whether to show a sign-in form or a
// first-run form. It discloses nothing else.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	count, err := s.users.Count(r.Context())
	if err != nil {
		s.logger.ErrorContext(r.Context(), "counting users failed", "error", err)
		httpapi.WriteError(w, r, api.CodeUnavailable, "the server is not able to answer right now")
		return
	}

	providers := make([]string, 0, len(s.cfg.Auth.Providers))
	for _, p := range s.cfg.Auth.Providers {
		providers = append(providers, string(p))
	}

	httpapi.WriteJSON(w, r, http.StatusOK, api.AuthStatus{
		BootstrapRequired: count == 0,
		Providers:         providers,
	})
}

// handleBootstrap creates the first administrator on a fresh installation.
//
// The claim is exactly-once: the metadata row and the account are inserted in
// one transaction, and the row insert is conditional, so however many requests
// arrive at once only one can create an administrator.
//
// This endpoint is open until it is claimed, which is a deliberate trade-off
// and a real one: a publicly reachable, unclaimed server can be taken by
// whoever finds it first. It buys the "deploy, open the web UI, create an
// account" flow the project is built around. The mitigations are that the
// window is visible — the server warns on every start until it is closed —
// that the claim is audited, and that the endpoint is behind the tighter
// authentication rate limit. See docs/security/threat-model.md.
func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	var req api.BootstrapRequest
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		return
	}

	now := s.clock.Now()
	var user *auth.User

	err := s.db.InTx(r.Context(), func(tx *sql.Tx) error {
		claimed, err := storage.ClaimBootstrap(r.Context(), s.db, tx, now)
		if err != nil {
			return err
		}
		if !claimed {
			return errAlreadyClaimed
		}

		user, err = s.users.Create(r.Context(), tx, auth.NewUser{
			Email:       req.Email,
			Password:    req.Password,
			DisplayName: req.DisplayName,
			// The first account is necessarily an administrator; there is
			// nobody else to grant it.
			Role: auth.RoleAdmin,
		}, now)
		return err
	})

	switch {
	case errors.Is(err, errAlreadyClaimed):
		httpapi.WriteError(w, r, api.CodeConflict,
			"this server already has an administrator; sign in instead")
		return
	case errors.Is(err, auth.ErrInvalidEmail),
		errors.Is(err, auth.ErrPasswordTooShort),
		errors.Is(err, auth.ErrPasswordTooLong),
		errors.Is(err, auth.ErrEmailTaken):
		httpapi.WriteError(w, r, api.CodeBadRequest, err.Error())
		return
	case err != nil:
		s.logger.ErrorContext(r.Context(), "bootstrapping the installation failed", "error", err)
		httpapi.WriteError(w, r, api.CodeInternal, "internal error")
		return
	}

	// Audited with its source address: claiming an installation is the single
	// most consequential action anyone takes against it.
	s.logger.InfoContext(r.Context(), "installation claimed; first administrator created",
		"user_id", user.ID, "remote_ip", httpapi.ClientIP(r))

	issued, err := s.sessions.Issue(r.Context(), user.ID, auth.Meta{
		IP:        httpapi.ClientIP(r),
		UserAgent: r.UserAgent(),
	}, now)
	if err != nil {
		// The account exists, so this is not fatal — they can simply sign in.
		s.logger.ErrorContext(r.Context(), "issuing a session after bootstrap failed", "error", err)
		httpapi.WriteJSON(w, r, http.StatusCreated, sessionResponse(user, "", time.Time{}))
		return
	}

	s.setSessionCookies(w, issued)
	httpapi.WriteJSON(w, r, http.StatusCreated,
		sessionResponse(user, issued.CSRFToken, issued.Session.ExpiresAt))
}

// errAlreadyClaimed signals that another caller won the bootstrap race.
var errAlreadyClaimed = errors.New("the installation has already been claimed")

// handleLogin authenticates a user and issues a session.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req api.LoginRequest
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		return
	}

	now := s.clock.Now()
	user, err := s.users.ByEmail(r.Context(), req.Email)
	if err != nil && !errors.Is(err, auth.ErrUserNotFound) {
		s.logger.ErrorContext(r.Context(), "looking up a user failed", "error", err)
		httpapi.WriteError(w, r, api.CodeInternal, "internal error")
		return
	}

	// Verify a password in every case, including when there is no account and
	// when the account has no local password. Returning early would make the
	// response measurably faster for an unregistered address, and the timing
	// difference is enough to enumerate users.
	authenticated, needsRehash := s.verifyCredentials(r, user, req.Password)
	if !authenticated {
		s.logger.WarnContext(r.Context(), "failed sign-in attempt",
			"remote_ip", httpapi.ClientIP(r))
		httpapi.WriteError(w, r, api.CodeUnauthorized, invalidCredentials)
		return
	}

	// The password is already known to be correct, so this is an upgrade to
	// current cost rather than a change. A failure here must not fail the
	// login: the user did nothing wrong.
	if needsRehash {
		if err := s.users.RehashPassword(r.Context(), user.ID, req.Password, now); err != nil {
			s.logger.WarnContext(r.Context(), "rehashing a password at current cost failed",
				"user_id", user.ID, "error", err)
		}
	}

	issued, err := s.sessions.Issue(r.Context(), user.ID, auth.Meta{
		IP:        httpapi.ClientIP(r),
		UserAgent: r.UserAgent(),
	}, now)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "issuing a session failed", "error", err)
		httpapi.WriteError(w, r, api.CodeInternal, "internal error")
		return
	}

	if err := s.users.RecordLogin(r.Context(), user.ID, now); err != nil {
		// Not worth failing the login over; the session is already valid.
		s.logger.WarnContext(r.Context(), "recording a login failed", "user_id", user.ID, "error", err)
	}

	s.logger.InfoContext(r.Context(), "user signed in",
		"user_id", user.ID, "session_id", issued.Session.ID, "remote_ip", httpapi.ClientIP(r))

	s.setSessionCookies(w, issued)
	httpapi.WriteJSON(w, r, http.StatusOK, sessionResponse(user, issued.CSRFToken, issued.Session.ExpiresAt))
}

// verifyCredentials checks a password, doing equivalent work whether or not
// the account exists.
func (s *Server) verifyCredentials(r *http.Request, user *auth.User, password string) (ok, needsRehash bool) {
	if user == nil || !user.HasPassword() {
		// Burn the same Argon2 time against a hash of a random password, so
		// that "no such account" costs what "wrong password" costs.
		if _, _, err := auth.VerifyPassword(password, s.dummyHash); err != nil {
			s.logger.ErrorContext(r.Context(), "the dummy password hash is unusable", "error", err)
		}
		return false, false
	}

	matched, needsRehash, err := user.VerifyPassword(password)
	if err != nil {
		// A malformed stored hash is an operator problem, not a wrong
		// password. The user still sees the generic message, but this needs to
		// be visible in the log or the account is silently unusable.
		s.logger.ErrorContext(r.Context(), "a stored password hash could not be read",
			"user_id", user.ID, "error", err)
		return false, false
	}
	return matched, needsRehash
}

// handleLogout ends the caller's session.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	// Clear the cookies before anything writes a status. Set-Cookie is a
	// header, so deferring this until after WriteHeader would silently do
	// nothing and leave the browser still holding the token. Doing it first
	// also means the browser drops the token even if the revocation below
	// fails.
	s.clearSessionCookies(w)

	cookie, err := r.Cookie(auth.SessionCookieName)
	if err == nil && cookie.Value != "" {
		if err := s.sessions.Revoke(r.Context(), cookie.Value); err != nil {
			s.logger.ErrorContext(r.Context(), "revoking a session failed", "error", err)
			httpapi.WriteError(w, r, api.CodeInternal, "internal error")
			return
		}
	}

	if session, ok := SessionFrom(r.Context()); ok {
		s.logger.InfoContext(r.Context(), "user signed out",
			"user_id", session.UserID, "session_id", session.ID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSession returns the caller's current session.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFrom(r.Context())
	if !ok {
		httpapi.WriteError(w, r, api.CodeUnauthorized, "you are not signed in")
		return
	}
	session, _ := SessionFrom(r.Context())

	// The CSRF token is not stored in a recoverable form, so it cannot be
	// returned here. A browser client reads it from its cookie; this response
	// carries the expiry and the account.
	httpapi.WriteJSON(w, r, http.StatusOK, sessionResponse(user, "", session.ExpiresAt))
}

// sessionResponse builds the wire representation of a signed-in session.
func sessionResponse(user *auth.User, csrfToken string, expiresAt time.Time) api.Session {
	return api.Session{
		User:      userResponse(user),
		CSRFToken: csrfToken,
		ExpiresAt: expiresAt,
	}
}

// userResponse converts an account to its wire form. It exists so that adding
// a field to auth.User cannot silently publish it.
func userResponse(user *auth.User) api.User {
	return api.User{
		ID:          user.ID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		Role:        string(user.Role),
		Provider:    string(user.Provider),
		CreatedAt:   user.CreatedAt,
		LastLoginAt: user.LastLoginAt,
	}
}

// setSessionCookies writes the session and CSRF cookies.
func (s *Server) setSessionCookies(w http.ResponseWriter, issued *auth.Issued) {
	maxAge := int(time.Until(issued.Session.ExpiresAt).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}

	http.SetCookie(w, &http.Cookie{
		Name:  auth.SessionCookieName,
		Value: issued.Token,
		Path:  "/",
		// No script needs to read this, so an XSS bug cannot exfiltrate it.
		HttpOnly: true,
		Secure:   s.secureCookies,
		// Lax rather than Strict: Strict would drop the cookie when a user
		// follows a link into the UI from elsewhere, which reads as being
		// randomly signed out. Lax still withholds it from cross-site POSTs,
		// and the CSRF token covers what remains.
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	})

	http.SetCookie(w, &http.Cookie{
		Name:  auth.CSRFCookieName,
		Value: issued.CSRFToken,
		Path:  "/",
		// Deliberately readable: the browser client has to echo this back in a
		// header, and that echo is the entire mechanism.
		HttpOnly: false,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	})
}

// clearSessionCookies expires both cookies in the browser.
func (s *Server) clearSessionCookies(w http.ResponseWriter) {
	for _, name := range []string{auth.SessionCookieName, auth.CSRFCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: name == auth.SessionCookieName,
			Secure:   s.secureCookies,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})
	}
}
