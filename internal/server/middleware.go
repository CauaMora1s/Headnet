package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/CauaMora1s/Headnet/internal/auth"
	"github.com/CauaMora1s/Headnet/internal/httpapi"
	"github.com/CauaMora1s/Headnet/packages/api"
)

// contextKey is unexported so nothing outside this package can collide with
// or forge these values.
type contextKey int

const (
	userContextKey contextKey = iota
	sessionContextKey
)

// UserFrom returns the signed-in user, if the request passed RequireSession.
func UserFrom(ctx context.Context) (*auth.User, bool) {
	user, ok := ctx.Value(userContextKey).(*auth.User)
	return user, ok && user != nil
}

// SessionFrom returns the caller's session, if the request passed
// RequireSession.
func SessionFrom(ctx context.Context) (*auth.Session, bool) {
	session, ok := ctx.Value(sessionContextKey).(*auth.Session)
	return session, ok && session != nil
}

// requireSession rejects a request that does not carry a valid session, and
// puts the session and its user into the request context.
//
// An unknown token and an expired one produce the same response. Telling them
// apart would confirm to an attacker that a guessed token once existed, which
// is information worth nothing to a legitimate client.
func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(auth.SessionCookieName)
		if err != nil || cookie.Value == "" {
			s.clearSessionCookies(w)
			httpapi.WriteError(w, r, api.CodeUnauthorized, "you are not signed in")
			return
		}

		session, err := s.sessions.Lookup(r.Context(), cookie.Value, s.clock.Now())
		if err != nil {
			if !errors.Is(err, auth.ErrSessionNotFound) && !errors.Is(err, auth.ErrSessionExpired) {
				s.logger.ErrorContext(r.Context(), "looking up a session failed", "error", err)
				httpapi.WriteError(w, r, api.CodeInternal, "internal error")
				return
			}
			// The browser is holding a token that is no longer any use;
			// clearing it stops every subsequent request from retrying it.
			s.clearSessionCookies(w)
			httpapi.WriteError(w, r, api.CodeUnauthorized, "your session has ended; sign in again")
			return
		}

		user, err := s.users.ByID(r.Context(), session.UserID)
		if err != nil {
			if errors.Is(err, auth.ErrUserNotFound) {
				// The account was deleted while the session lived. The
				// cascade should have removed the session too, so this is
				// worth recording rather than quietly tolerating.
				s.logger.WarnContext(r.Context(), "a session outlived its user",
					"session_id", session.ID, "user_id", session.UserID)
				s.clearSessionCookies(w)
				httpapi.WriteError(w, r, api.CodeUnauthorized, "your session has ended; sign in again")
				return
			}
			s.logger.ErrorContext(r.Context(), "loading the session user failed", "error", err)
			httpapi.WriteError(w, r, api.CodeInternal, "internal error")
			return
		}

		ctx := context.WithValue(r.Context(), sessionContextKey, session)
		ctx = context.WithValue(ctx, userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireCSRF rejects a state-changing request that does not echo the
// session's CSRF token.
//
// Safe methods are exempt: they do not change state, and requiring a header on
// them would break ordinary navigation for no benefit. Any handler that
// changes state behind a GET is a bug this middleware cannot compensate for.
func (s *Server) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		session, ok := SessionFrom(r.Context())
		if !ok {
			// requireSession runs first, so reaching here is a wiring mistake
			// rather than a client error.
			s.logger.ErrorContext(r.Context(), "the CSRF check ran without a session in context")
			httpapi.WriteError(w, r, api.CodeInternal, "internal error")
			return
		}

		if !session.VerifyCSRF(r.Header.Get(auth.CSRFHeaderName)) {
			httpapi.WriteErrorDetail(w, r, api.CodeForbidden,
				"this request is missing a valid CSRF token",
				"header", auth.CSRFHeaderName)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireAdmin rejects a caller who is not an administrator.
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFrom(r.Context())
		if !ok {
			s.logger.ErrorContext(r.Context(), "the admin check ran without a user in context")
			httpapi.WriteError(w, r, api.CodeInternal, "internal error")
			return
		}
		if !user.IsAdmin() {
			httpapi.WriteError(w, r, api.CodeForbidden,
				"this action requires an administrator account")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authenticated composes the session, CSRF and (optionally) admin checks in
// the one order that is correct: identify the caller, then verify the request
// was intended, then check permission.
func (s *Server) authenticated(h http.Handler, adminOnly bool) http.Handler {
	if adminOnly {
		h = s.requireAdmin(h)
	}
	return s.requireSession(s.requireCSRF(h))
}

// rateLimitAuth applies the tighter authentication budget.
//
// Login and bootstrap are unauthenticated by necessity, and repeated guessing
// is the entire attack against them, so they get a much smaller allowance than
// the rest of the API.
func (s *Server) rateLimitAuth(next http.Handler) http.Handler {
	if s.authLimiter == nil {
		return next
	}
	return httpapi.RateLimit(s.authLimiter)(next)
}
