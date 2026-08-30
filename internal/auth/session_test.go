package auth_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/CauaMora1s/Headnet/internal/auth"
	"github.com/CauaMora1s/Headnet/internal/storage"
	"github.com/CauaMora1s/Headnet/internal/storage/storagetest"
)

// newSessions returns a session store and a user to attach sessions to.
func newSessions(t *testing.T, opts auth.SessionOptions) (*auth.SessionStore, *auth.User, *storage.DB) {
	t.Helper()
	db := storagetest.Open(t)
	users := auth.NewUserStore(db, auth.StoreOptions{Params: cheapParams()})

	var user *auth.User
	err := db.InTx(t.Context(), func(tx *sql.Tx) error {
		var err error
		user, err = users.Create(t.Context(), tx,
			auth.NewUser{Email: "ada@example.com", Password: goodPassword}, time.Now().UTC())
		return err
	})
	if err != nil {
		t.Fatalf("creating the user failed: %v", err)
	}
	return auth.NewSessionStore(db, opts), user, db
}

func TestIssueAndLookup(t *testing.T) {
	sessions, user, _ := newSessions(t, auth.SessionOptions{})
	now := time.Now().UTC()

	issued, err := sessions.Issue(t.Context(), user.ID, auth.Meta{IP: "192.0.2.1", UserAgent: "test"}, now)
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}
	if issued.Token == "" || issued.CSRFToken == "" {
		t.Fatal("Issue returned an empty token")
	}
	if issued.Token == issued.CSRFToken {
		t.Fatal("the session token and the CSRF token are the same value")
	}

	found, err := sessions.Lookup(t.Context(), issued.Token, now)
	if err != nil {
		t.Fatalf("Lookup failed: %v", err)
	}
	if found.UserID != user.ID {
		t.Fatalf("UserID = %q, want %q", found.UserID, user.ID)
	}
	if found.IP != "192.0.2.1" {
		t.Errorf("IP = %q, want it recorded", found.IP)
	}
}

func TestNeitherTokenIsRecoverableFromTheDatabase(t *testing.T) {
	// The property that makes a stolen database not a stolen session. Only
	// SHA-256 of each secret is stored, so nothing there can be replayed.
	sessions, user, db := newSessions(t, auth.SessionOptions{})

	issued, err := sessions.Issue(t.Context(), user.ID, auth.Meta{}, time.Now().UTC())
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}

	var tokenHash, csrfHash string
	err = db.QueryRowContext(t.Context(),
		`SELECT token_hash, csrf_hash FROM sessions`).Scan(&tokenHash, &csrfHash)
	if err != nil {
		t.Fatalf("reading the sessions table failed: %v", err)
	}

	for _, stored := range []string{tokenHash, csrfHash} {
		if strings.Contains(stored, issued.Token) || strings.Contains(stored, issued.CSRFToken) {
			t.Fatalf("a raw token was stored in the database: %q", stored)
		}
		if len(stored) != 64 {
			t.Fatalf("stored value %q is not a hex SHA-256 digest", stored)
		}
	}
}

func TestTheCSRFHashCannotEscapeTheStruct(t *testing.T) {
	// Same reasoning as the password hash on User: unexported keeps it from a
	// JSON encoder, String and GoString keep it out of %+v.
	sessions, user, _ := newSessions(t, auth.SessionOptions{})
	issued, err := sessions.Issue(t.Context(), user.ID, auth.Meta{}, time.Now().UTC())
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}

	encoded, err := json.Marshal(issued.Session)
	if err != nil {
		t.Fatalf("marshalling failed: %v", err)
	}

	csrfHash := issued.Session.String()
	_ = csrfHash
	for _, rendered := range []string{
		string(encoded),
		fmt.Sprintf("%v", issued.Session),
		fmt.Sprintf("%+v", issued.Session),
		fmt.Sprintf("%#v", issued.Session),
	} {
		if strings.Contains(rendered, "csrf") && strings.Contains(rendered, "Hash") {
			t.Fatalf("the CSRF hash appeared in rendered output:\n%s", rendered)
		}
	}
	if strings.Contains(fmt.Sprintf("%+v", issued.Session), issued.CSRFToken) {
		t.Fatal("the CSRF token itself appeared in formatted output")
	}
}

func TestUnknownTokenIsIndistinguishableFromExpired(t *testing.T) {
	// Telling them apart would confirm to an attacker that a guessed token
	// once existed. Both are errors the handler maps to the same response.
	sessions, user, _ := newSessions(t, auth.SessionOptions{Lifetime: time.Hour})
	now := time.Now().UTC()

	issued, err := sessions.Issue(t.Context(), user.ID, auth.Meta{}, now)
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}

	_, unknownErr := sessions.Lookup(t.Context(), "not-a-real-token", now)
	if !errors.Is(unknownErr, auth.ErrSessionNotFound) {
		t.Fatalf("unknown token error = %v, want ErrSessionNotFound", unknownErr)
	}

	_, expiredErr := sessions.Lookup(t.Context(), issued.Token, now.Add(2*time.Hour))
	if !errors.Is(expiredErr, auth.ErrSessionExpired) {
		t.Fatalf("expired token error = %v, want ErrSessionExpired", expiredErr)
	}
}

func TestAbsoluteExpiryEndsASession(t *testing.T) {
	sessions, user, _ := newSessions(t, auth.SessionOptions{Lifetime: time.Hour})
	now := time.Now().UTC()

	issued, err := sessions.Issue(t.Context(), user.ID, auth.Meta{}, now)
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}

	// Still valid a minute before expiry.
	if _, err := sessions.Lookup(t.Context(), issued.Token, now.Add(59*time.Minute)); err != nil {
		t.Fatalf("the session expired early: %v", err)
	}
	// Gone at expiry.
	if _, err := sessions.Lookup(t.Context(), issued.Token, now.Add(time.Hour)); !errors.Is(err, auth.ErrSessionExpired) {
		t.Fatalf("error = %v, want ErrSessionExpired", err)
	}
}

func TestIdleTimeoutEndsASession(t *testing.T) {
	sessions, user, _ := newSessions(t, auth.SessionOptions{
		Lifetime: 30 * 24 * time.Hour, IdleTimeout: time.Hour,
	})
	now := time.Now().UTC()

	issued, err := sessions.Issue(t.Context(), user.ID, auth.Meta{}, now)
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}

	if _, err := sessions.Lookup(t.Context(), issued.Token, now.Add(2*time.Hour)); !errors.Is(err, auth.ErrSessionExpired) {
		t.Fatalf("error = %v, want the idle timeout to end the session", err)
	}
}

func TestUsingASessionSlidesTheIdleWindow(t *testing.T) {
	sessions, user, _ := newSessions(t, auth.SessionOptions{
		Lifetime: 30 * 24 * time.Hour, IdleTimeout: time.Hour,
	})
	now := time.Now().UTC()

	issued, err := sessions.Issue(t.Context(), user.ID, auth.Meta{}, now)
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}

	// Used every 45 minutes: never idle for a full hour, so it survives well
	// past the idle timeout in absolute terms.
	at := now
	for range 5 {
		at = at.Add(45 * time.Minute)
		if _, err := sessions.Lookup(t.Context(), issued.Token, at); err != nil {
			t.Fatalf("an actively used session expired at %v: %v", at.Sub(now), err)
		}
	}
}

func TestAnExpiredSessionIsDeletedWhenFound(t *testing.T) {
	// Expiry cleans up as a side effect of normal traffic, so correctness does
	// not depend on a sweeper having run.
	sessions, user, db := newSessions(t, auth.SessionOptions{Lifetime: time.Hour})
	now := time.Now().UTC()

	issued, err := sessions.Issue(t.Context(), user.ID, auth.Meta{}, now)
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}
	if _, err := sessions.Lookup(t.Context(), issued.Token, now.Add(2*time.Hour)); err == nil {
		t.Fatal("the expired session was accepted")
	}

	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatalf("counting sessions failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("%d expired sessions remain in the table", count)
	}
}

func TestRevokeEndsASessionImmediately(t *testing.T) {
	// The reason sessions are stored server-side at all: when a laptop is
	// stolen, access has to stop now, not when a token happens to expire.
	sessions, user, _ := newSessions(t, auth.SessionOptions{})
	now := time.Now().UTC()

	issued, err := sessions.Issue(t.Context(), user.ID, auth.Meta{}, now)
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}
	if err := sessions.Revoke(t.Context(), issued.Token); err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}
	if _, err := sessions.Lookup(t.Context(), issued.Token, now); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatalf("error = %v, want the revoked session to be gone", err)
	}
}

func TestRevokingTwiceIsNotAnError(t *testing.T) {
	sessions, user, _ := newSessions(t, auth.SessionOptions{})
	issued, err := sessions.Issue(t.Context(), user.ID, auth.Meta{}, time.Now().UTC())
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}
	for range 2 {
		if err := sessions.Revoke(t.Context(), issued.Token); err != nil {
			t.Fatalf("Revoke failed: %v", err)
		}
	}
	if err := sessions.Revoke(t.Context(), ""); err != nil {
		t.Fatalf("revoking an empty token errored: %v", err)
	}
}

func TestRevokeAllForUser(t *testing.T) {
	sessions, user, _ := newSessions(t, auth.SessionOptions{})
	now := time.Now().UTC()

	var tokens []string
	for range 3 {
		issued, err := sessions.Issue(t.Context(), user.ID, auth.Meta{}, now)
		if err != nil {
			t.Fatalf("Issue failed: %v", err)
		}
		tokens = append(tokens, issued.Token)
	}

	revoked, err := sessions.RevokeAllForUser(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("RevokeAllForUser failed: %v", err)
	}
	if revoked != 3 {
		t.Fatalf("revoked %d sessions, want 3", revoked)
	}
	for i, token := range tokens {
		if _, err := sessions.Lookup(t.Context(), token, now); err == nil {
			t.Fatalf("session %d still works after a mass revocation", i)
		}
	}
}

func TestDeletingAUserDeletesTheirSessions(t *testing.T) {
	// Enforced by ON DELETE CASCADE, which is why foreign keys are pinned on
	// for SQLite. A session that outlived its user would be a live credential
	// for an account that no longer exists.
	sessions, user, db := newSessions(t, auth.SessionOptions{})
	now := time.Now().UTC()

	issued, err := sessions.Issue(t.Context(), user.ID, auth.Meta{}, now)
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}

	if _, err := db.ExecContext(t.Context(),
		db.Rebind(`DELETE FROM users WHERE id = ?`), user.ID); err != nil {
		t.Fatalf("deleting the user failed: %v", err)
	}

	if _, err := sessions.Lookup(t.Context(), issued.Token, now); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatalf("error = %v, want the session to have been cascaded away", err)
	}
}

func TestCSRFVerification(t *testing.T) {
	sessions, user, _ := newSessions(t, auth.SessionOptions{})
	now := time.Now().UTC()

	first, err := sessions.Issue(t.Context(), user.ID, auth.Meta{}, now)
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}
	second, err := sessions.Issue(t.Context(), user.ID, auth.Meta{}, now)
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}

	loaded, err := sessions.Lookup(t.Context(), first.Token, now)
	if err != nil {
		t.Fatalf("Lookup failed: %v", err)
	}

	if !loaded.VerifyCSRF(first.CSRFToken) {
		t.Error("the session's own CSRF token did not verify")
	}
	// Binding the token to the session is what defeats an attacker who can set
	// a cookie on the victim's domain: they cannot supply a matching pair this
	// server issued for this session.
	if loaded.VerifyCSRF(second.CSRFToken) {
		t.Error("a CSRF token from another session verified")
	}
	for _, bad := range []string{"", "wrong", first.Token} {
		if loaded.VerifyCSRF(bad) {
			t.Errorf("the CSRF check accepted %q", bad)
		}
	}
}

func TestDeleteExpired(t *testing.T) {
	sessions, user, _ := newSessions(t, auth.SessionOptions{Lifetime: time.Hour})
	now := time.Now().UTC()

	for range 3 {
		if _, err := sessions.Issue(t.Context(), user.ID, auth.Meta{}, now); err != nil {
			t.Fatalf("Issue failed: %v", err)
		}
	}

	if deleted, err := sessions.DeleteExpired(t.Context(), now); err != nil || deleted != 0 {
		t.Fatalf("DeleteExpired on live sessions = (%d, %v), want (0, nil)", deleted, err)
	}
	deleted, err := sessions.DeleteExpired(t.Context(), now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("DeleteExpired failed: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("DeleteExpired removed %d sessions, want 3", deleted)
	}
}

func TestEverySessionGetsDistinctTokens(t *testing.T) {
	sessions, user, _ := newSessions(t, auth.SessionOptions{})
	now := time.Now().UTC()

	seen := make(map[string]struct{}, 40)
	for range 20 {
		issued, err := sessions.Issue(t.Context(), user.ID, auth.Meta{}, now)
		if err != nil {
			t.Fatalf("Issue failed: %v", err)
		}
		for _, token := range []string{issued.Token, issued.CSRFToken} {
			if _, dup := seen[token]; dup {
				t.Fatal("Issue produced a duplicate token")
			}
			seen[token] = struct{}{}
		}
	}
}

func TestIssueRejectsAnEmptyUser(t *testing.T) {
	sessions, _, _ := newSessions(t, auth.SessionOptions{})
	if _, err := sessions.Issue(t.Context(), "", auth.Meta{}, time.Now()); err == nil {
		t.Fatal("Issue accepted an empty user ID")
	}
}

func TestLookupRejectsAnEmptyToken(t *testing.T) {
	sessions, _, _ := newSessions(t, auth.SessionOptions{})
	if _, err := sessions.Lookup(t.Context(), "", time.Now()); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatalf("error = %v, want ErrSessionNotFound", err)
	}
}

func TestUserAgentIsBounded(t *testing.T) {
	// A user agent is attacker-supplied and otherwise unbounded.
	sessions, user, _ := newSessions(t, auth.SessionOptions{})
	issued, err := sessions.Issue(t.Context(), user.ID,
		auth.Meta{UserAgent: strings.Repeat("x", 10_000)}, time.Now().UTC())
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}
	if len(issued.Session.UserAgent) > 256 {
		t.Fatalf("the stored user agent is %d characters, want it truncated", len(issued.Session.UserAgent))
	}
}

func TestCountForUser(t *testing.T) {
	sessions, user, _ := newSessions(t, auth.SessionOptions{})
	now := time.Now().UTC()

	if n, err := sessions.CountForUser(t.Context(), user.ID); err != nil || n != 0 {
		t.Fatalf("CountForUser = (%d, %v), want (0, nil)", n, err)
	}
	for range 2 {
		if _, err := sessions.Issue(t.Context(), user.ID, auth.Meta{}, now); err != nil {
			t.Fatalf("Issue failed: %v", err)
		}
	}
	if n, err := sessions.CountForUser(t.Context(), user.ID); err != nil || n != 2 {
		t.Fatalf("CountForUser = (%d, %v), want (2, nil)", n, err)
	}
}
