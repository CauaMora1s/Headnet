package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/CauaMora1s/Headnet/internal/storage"
	"github.com/CauaMora1s/Headnet/packages/shared"
)

// SessionIDPrefix marks a session identifier in logs and audit records.
const SessionIDPrefix = "ses"

// Names of the cookies and header that carry session state.
const (
	// SessionCookieName holds the session token. It is HttpOnly: no script
	// ever needs to read it, and keeping it unreadable means an XSS bug
	// cannot simply exfiltrate the session.
	SessionCookieName = "headnet_session"

	// CSRFCookieName holds the CSRF token. This one is deliberately readable
	// by script, because the browser client has to echo it back in a header —
	// that echo is the whole mechanism. Its safety rests on the same-origin
	// policy: a cross-site attacker can cause a request to be sent with the
	// cookies attached, but cannot read them to construct the header.
	CSRFCookieName = "headnet_csrf"

	// CSRFHeaderName is where the client echoes the CSRF token.
	CSRFHeaderName = "X-CSRF-Token"
)

// Token sizes in bytes. 32 bytes of CSPRNG output is far beyond guessable and
// is the conventional size for a bearer credential.
const (
	sessionTokenBytes = 32
	csrfTokenBytes    = 32
)

// sessionTouchInterval is how stale last_used_at may become before a request
// writes it back.
//
// Updating it on every request would mean a database write per request, which
// on SQLite — held to a single connection — would serialise the whole server
// behind session bookkeeping. A minute of imprecision on an idle timeout
// measured in days costs nothing.
const sessionTouchInterval = time.Minute

// Session errors.
var (
	// ErrSessionNotFound means the token matched nothing. Handlers must treat
	// it identically to an expired session; distinguishing them tells an
	// attacker whether a guessed token ever existed.
	ErrSessionNotFound = errors.New("no such session")

	// ErrSessionExpired means the session existed but is no longer valid.
	ErrSessionExpired = errors.New("the session has expired")
)

// Session is a signed-in browser or client.
//
// The CSRF hash is unexported for the same reason as the password hash on
// User: it must not reach a JSON encoder, a template, or a %+v in a log call.
type Session struct {
	ID         string
	UserID     string
	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time
	IP         string
	UserAgent  string

	csrfHash string
}

// String renders the session without its CSRF hash.
func (s Session) String() string {
	return fmt.Sprintf("Session{ID:%s UserID:%s ExpiresAt:%s}",
		s.ID, s.UserID, s.ExpiresAt.Format(time.RFC3339))
}

// GoString covers %#v for the same reason as String.
func (s Session) GoString() string { return s.String() }

// VerifyCSRF checks a token echoed by the client against this session.
//
// The token is bound to the session rather than merely being compared with a
// cookie. Plain double-submit — trusting that a cookie and a header match —
// can be defeated by an attacker who can set a cookie on the victim's domain,
// because they can then choose both halves. Storing the hash server-side means
// only a token this server issued for this session will do.
func (s *Session) VerifyCSRF(token string) bool {
	if token == "" || s.csrfHash == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(HashToken(token)), []byte(s.csrfHash)) == 1
}

// Issued is a newly created session together with the two secrets handed to
// the client. They exist only here and in the client; the database holds only
// their hashes.
type Issued struct {
	Session   Session
	Token     string
	CSRFToken string
}

// Meta records where a session was created from, for the session list an
// operator uses to spot one they do not recognise.
type Meta struct {
	IP        string
	UserAgent string
}

// maxUserAgentLength bounds what is stored. A user agent is attacker-supplied
// and otherwise unbounded.
const maxUserAgentLength = 256

// SessionOptions configure a SessionStore.
type SessionOptions struct {
	// Lifetime is the absolute maximum age of a session, regardless of use.
	Lifetime time.Duration
	// IdleTimeout ends a session that has gone unused for this long. Zero
	// disables idle expiry.
	IdleTimeout time.Duration
}

// SessionStore issues and validates sessions.
type SessionStore struct {
	db          *storage.DB
	lifetime    time.Duration
	idleTimeout time.Duration
}

// Default session durations, used when Options leaves them zero.
const (
	DefaultSessionLifetime    = 30 * 24 * time.Hour
	DefaultSessionIdleTimeout = 7 * 24 * time.Hour
)

// NewSessionStore builds a store over an open database.
func NewSessionStore(db *storage.DB, opts SessionOptions) *SessionStore {
	lifetime := opts.Lifetime
	if lifetime <= 0 {
		lifetime = DefaultSessionLifetime
	}
	idle := opts.IdleTimeout
	if idle < 0 {
		idle = 0
	}
	return &SessionStore{db: db, lifetime: lifetime, idleTimeout: idle}
}

// Issue creates a session for a user.
//
// A fresh session is always created rather than any existing one being reused
// or upgraded, which is what makes session fixation impossible: an attacker
// who somehow planted a token cannot have it become an authenticated one.
func (s *SessionStore) Issue(ctx context.Context, userID string, meta Meta, now time.Time) (*Issued, error) {
	if userID == "" {
		return nil, errors.New("auth: a user ID is required to issue a session")
	}

	token, err := NewToken(sessionTokenBytes)
	if err != nil {
		return nil, err
	}
	csrfToken, err := NewToken(csrfTokenBytes)
	if err != nil {
		return nil, err
	}

	userAgent := meta.UserAgent
	if len(userAgent) > maxUserAgentLength {
		userAgent = userAgent[:maxUserAgentLength]
	}

	session := Session{
		ID:         shared.NewID(SessionIDPrefix),
		UserID:     userID,
		CreatedAt:  now,
		LastUsedAt: now,
		ExpiresAt:  now.Add(s.lifetime),
		IP:         meta.IP,
		UserAgent:  userAgent,
		csrfHash:   HashToken(csrfToken),
	}

	_, err = s.db.ExecContext(ctx, s.db.Rebind(`
		INSERT INTO sessions (id, token_hash, csrf_hash, user_id, created_at, last_used_at, expires_at, ip, user_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		session.ID, HashToken(token), session.csrfHash, session.UserID,
		session.CreatedAt, session.LastUsedAt, session.ExpiresAt, session.IP, session.UserAgent,
	)
	if err != nil {
		return nil, fmt.Errorf("creating the session: %w", err)
	}

	return &Issued{Session: session, Token: token, CSRFToken: csrfToken}, nil
}

// Lookup validates a session token and returns the session.
//
// An expired session is deleted as it is found, so expiry cleans up as a side
// effect of normal traffic rather than needing a sweeper to be correct.
func (s *SessionStore) Lookup(ctx context.Context, token string, now time.Time) (*Session, error) {
	if token == "" {
		return nil, ErrSessionNotFound
	}

	var (
		session   Session
		expiresAt time.Time
	)
	err := s.db.QueryRowContext(ctx, s.db.Rebind(`
		SELECT id, csrf_hash, user_id, created_at, last_used_at, expires_at, ip, user_agent
		FROM sessions WHERE token_hash = ?`), HashToken(token)).Scan(
		&session.ID, &session.csrfHash, &session.UserID, &session.CreatedAt,
		&session.LastUsedAt, &expiresAt, &session.IP, &session.UserAgent,
	)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrSessionNotFound
	case err != nil:
		return nil, fmt.Errorf("reading the session: %w", err)
	}
	session.ExpiresAt = expiresAt

	if s.isExpired(session, now) {
		if err := s.deleteByID(ctx, session.ID); err != nil {
			return nil, err
		}
		return nil, ErrSessionExpired
	}

	// Slide the idle window, but only when the recorded value has actually
	// gone stale; see sessionTouchInterval.
	if now.Sub(session.LastUsedAt) > sessionTouchInterval {
		if _, err := s.db.ExecContext(ctx,
			s.db.Rebind(`UPDATE sessions SET last_used_at = ? WHERE id = ?`), now, session.ID); err != nil {
			return nil, fmt.Errorf("refreshing the session: %w", err)
		}
		session.LastUsedAt = now
	}
	return &session, nil
}

func (s *SessionStore) isExpired(session Session, now time.Time) bool {
	if !now.Before(session.ExpiresAt) {
		return true
	}
	return s.idleTimeout > 0 && now.Sub(session.LastUsedAt) >= s.idleTimeout
}

// Revoke ends a session by its token. Revoking one that does not exist is not
// an error: logging out twice should not fail.
func (s *SessionStore) Revoke(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		s.db.Rebind(`DELETE FROM sessions WHERE token_hash = ?`), HashToken(token))
	if err != nil {
		return fmt.Errorf("revoking the session: %w", err)
	}
	return nil
}

// RevokeAllForUser ends every session belonging to a user and reports how many
// were ended. This is what a password change and a "sign out everywhere" both
// call, and what makes a stolen laptop recoverable.
func (s *SessionStore) RevokeAllForUser(ctx context.Context, userID string) (int, error) {
	result, err := s.db.ExecContext(ctx,
		s.db.Rebind(`DELETE FROM sessions WHERE user_id = ?`), userID)
	if err != nil {
		return 0, fmt.Errorf("revoking the sessions of %s: %w", userID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("revoking the sessions of %s: %w", userID, err)
	}
	return int(affected), nil
}

// DeleteExpired removes sessions past their absolute expiry and reports how
// many were removed.
//
// Lookup already refuses and deletes an expired session as it encounters one,
// so this is housekeeping for sessions nobody ever comes back to — not a
// correctness requirement.
func (s *SessionStore) DeleteExpired(ctx context.Context, now time.Time) (int, error) {
	result, err := s.db.ExecContext(ctx,
		s.db.Rebind(`DELETE FROM sessions WHERE expires_at <= ?`), now)
	if err != nil {
		return 0, fmt.Errorf("deleting expired sessions: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("deleting expired sessions: %w", err)
	}
	return int(affected), nil
}

// CountForUser reports how many live sessions a user has.
func (s *SessionStore) CountForUser(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		s.db.Rebind(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`), userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting sessions: %w", err)
	}
	return n, nil
}

func (s *SessionStore) deleteByID(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx,
		s.db.Rebind(`DELETE FROM sessions WHERE id = ?`), id); err != nil {
		return fmt.Errorf("deleting the expired session: %w", err)
	}
	return nil
}

// NewToken returns n bytes of CSPRNG output, base64url-encoded without padding
// so it is safe in a cookie, a header, a URL and a shell argument.
//
// Exported because setup keys need exactly the same thing. Two copies of a
// token generator is two places to get the entropy or the encoding wrong.
func NewToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: generating a token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashToken hashes a bearer token for storage.
//
// SHA-256, not a password KDF. The input is 32 bytes of CSPRNG output, so
// there is no low-entropy guess for a slow hash to frustrate; all this needs
// to do is ensure that reading the table yields nothing replayable.
//
// Exported alongside NewToken so setup keys store their credential the same
// way sessions store theirs.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
