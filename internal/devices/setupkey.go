package devices

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/CauaMora1s/Headnet/internal/auth"
	"github.com/CauaMora1s/Headnet/packages/shared"
)

// SetupKeyIDPrefix marks a setup key identifier in logs and audit records. The
// key itself is a separate value and is never an identifier.
const SetupKeyIDPrefix = "sk"

// setupKeyTokenBytes is the size of the secret. 32 bytes of CSPRNG output is
// the same strength as a session token, which is the right comparison: both
// are bearer credentials that grant access on presentation alone.
const setupKeyTokenBytes = 32

// displayHintLength is how much of a key is kept for recognition.
//
// Short enough to be useless on its own — 8 base64url characters is 48 bits,
// and an attacker holding it still faces the remaining 208 — but long enough
// that an operator can tell two keys apart in a list.
const displayHintLength = 8

// DefaultExpiry is applied when a key is created without one.
//
// Expiry is the default rather than the exception because a setup key ends up
// in shell histories, CI logs and chat messages, and one that never expires is
// one you will still be finding years later.
const DefaultExpiry = 7 * 24 * time.Hour

// Reasons a setup key cannot be redeemed.
//
// They are distinct so the server can log precisely what was wrong. The caller
// is told only that the key was rejected: telling an attacker whether a key
// was revoked, expired or merely used up confirms it once existed.
var (
	ErrKeyNotFound  = errors.New("no such setup key")
	ErrKeyRevoked   = errors.New("the setup key has been revoked")
	ErrKeyExpired   = errors.New("the setup key has expired")
	ErrKeyExhausted = errors.New("the setup key has been used the maximum number of times")
)

// SetupKey enrols a device without an interactive login.
//
// The hash is unexported, and String/GoString are implemented, for the same
// reason as on auth.User and auth.Session: fmt reads unexported fields, so
// making the field private is not on its own enough to keep it out of a log.
type SetupKey struct {
	ID          string
	DisplayHint string
	Description string
	// Tags carry the key's scope. Phase 6's policy engine will read them.
	// Nothing enforces them today.
	Tags      []string
	CreatedBy string
	CreatedAt time.Time
	// ExpiresAt is nil only when the creator explicitly asked for a key that
	// never expires.
	ExpiresAt *time.Time
	// MaxUses of 0 means unlimited.
	MaxUses int
	Uses    int
	// RevokedAt is nil while the key is live.
	RevokedAt *time.Time

	keyHash string
}

// String renders the key without its hash.
func (k SetupKey) String() string {
	return fmt.Sprintf("SetupKey{ID:%s Hint:%s Uses:%d/%d}",
		k.ID, k.DisplayHint, k.Uses, k.MaxUses)
}

// GoString covers %#v for the same reason as String.
func (k SetupKey) GoString() string { return k.String() }

// Revoked reports whether the key has been revoked.
func (k *SetupKey) Revoked() bool { return k.RevokedAt != nil }

// Expired reports whether the key has passed its expiry.
func (k *SetupKey) Expired(now time.Time) bool {
	return k.ExpiresAt != nil && !now.Before(*k.ExpiresAt)
}

// Exhausted reports whether the key has been used its maximum number of times.
func (k *SetupKey) Exhausted() bool { return k.MaxUses > 0 && k.Uses >= k.MaxUses }

// SingleUse reports whether the key may be redeemed exactly once.
func (k *SetupKey) SingleUse() bool { return k.MaxUses == 1 }

// Redeemable reports why a key cannot be used, or nil if it can be.
//
// The reasons are checked in the order an operator would want to hear them:
// revocation is a deliberate act and worth naming first.
func (k *SetupKey) Redeemable(now time.Time) error {
	switch {
	case k.Revoked():
		return ErrKeyRevoked
	case k.Expired(now):
		return ErrKeyExpired
	case k.Exhausted():
		return ErrKeyExhausted
	default:
		return nil
	}
}

// NewSetupKey describes a key to create.
type NewSetupKey struct {
	// Description is free text shown in the key list.
	Description string
	// Tags scope the key. Phase 6 reads them.
	Tags []string
	// ExpiresIn is how long the key stays valid. Zero means DefaultExpiry.
	ExpiresIn time.Duration
	// NeverExpires overrides ExpiresIn and creates a key with no expiry. It
	// exists so that choosing one is a deliberate act rather than an omission.
	NeverExpires bool
	// MaxUses caps redemptions. Zero means unlimited; one makes the key
	// single-use.
	MaxUses int
}

// IssuedSetupKey is a newly created key together with the secret, which is
// returned exactly once and never stored.
type IssuedSetupKey struct {
	Key SetupKey
	// Token is the value handed to the operator. The database holds only its
	// hash, so this cannot be recovered later.
	Token string
}

// maxDescriptionLength bounds operator-supplied free text.
const maxDescriptionLength = 200

// CreateSetupKey issues a key.
func (s *Store) CreateSetupKey(
	ctx context.Context, createdBy string, in NewSetupKey, now time.Time,
) (*IssuedSetupKey, error) {
	if createdBy == "" {
		return nil, errors.New("devices: a creating user is required")
	}
	if in.MaxUses < 0 {
		return nil, fmt.Errorf("devices: max uses must not be negative, got %d", in.MaxUses)
	}
	if in.ExpiresIn < 0 {
		return nil, fmt.Errorf("devices: expiry must not be negative, got %s", in.ExpiresIn)
	}

	token, err := auth.NewToken(setupKeyTokenBytes)
	if err != nil {
		return nil, err
	}

	key := SetupKey{
		ID:          shared.NewID(SetupKeyIDPrefix),
		DisplayHint: token[:displayHintLength],
		Description: truncate(strings.TrimSpace(in.Description), maxDescriptionLength),
		Tags:        normaliseTags(in.Tags),
		CreatedBy:   createdBy,
		CreatedAt:   now,
		MaxUses:     in.MaxUses,
		keyHash:     auth.HashToken(token),
	}

	// An omitted expiry becomes the default; only NeverExpires produces a key
	// with none.
	if !in.NeverExpires {
		lifetime := in.ExpiresIn
		if lifetime == 0 {
			lifetime = DefaultExpiry
		}
		expiry := now.Add(lifetime)
		key.ExpiresAt = &expiry
	}

	_, err = s.db.ExecContext(ctx, s.db.Rebind(`
		INSERT INTO setup_keys
			(id, key_hash, display_hint, description, tags, created_by, created_at, expires_at, max_uses, uses, revoked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, NULL)`),
		key.ID, key.keyHash, key.DisplayHint, key.Description, strings.Join(key.Tags, ","),
		key.CreatedBy, key.CreatedAt, key.ExpiresAt, key.MaxUses,
	)
	if err != nil {
		return nil, fmt.Errorf("creating the setup key: %w", err)
	}

	return &IssuedSetupKey{Key: key, Token: token}, nil
}

const setupKeyColumns = `id, key_hash, display_hint, description, tags, created_by,
	created_at, expires_at, max_uses, uses, revoked_at`

// SetupKeyByToken looks a key up by the secret an enroller presented.
func (s *Store) SetupKeyByToken(ctx context.Context, q Queryer, token string) (*SetupKey, error) {
	if token == "" {
		return nil, ErrKeyNotFound
	}
	if q == nil {
		q = s.db
	}
	return s.oneSetupKey(ctx, q,
		`SELECT `+setupKeyColumns+` FROM setup_keys WHERE key_hash = ?`, auth.HashToken(token))
}

// SetupKeyByID looks a key up by identifier.
func (s *Store) SetupKeyByID(ctx context.Context, id string) (*SetupKey, error) {
	return s.oneSetupKey(ctx, s.db, `SELECT `+setupKeyColumns+` FROM setup_keys WHERE id = ?`, id)
}

// ListSetupKeys returns every key, newest first.
func (s *Store) ListSetupKeys(ctx context.Context) ([]SetupKey, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+setupKeyColumns+` FROM setup_keys ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("listing setup keys: %w", err)
	}
	defer rows.Close()

	var out []SetupKey
	for rows.Next() {
		key, err := scanSetupKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing setup keys: %w", err)
	}
	return out, nil
}

// RevokeSetupKey ends a key immediately. Revoking one twice is not an error;
// the first revocation stands, so the recorded time is when it actually
// stopped working.
func (s *Store) RevokeSetupKey(ctx context.Context, id string, now time.Time) error {
	result, err := s.db.ExecContext(ctx, s.db.Rebind(
		`UPDATE setup_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`), now, id)
	if err != nil {
		return fmt.Errorf("revoking the setup key: %w", err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		// Either it does not exist or it was already revoked. Distinguish them
		// so the caller can return a sensible status.
		if _, err := s.SetupKeyByID(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// consumeSetupKey records one redemption, refusing if the key has been used up
// in the meantime.
//
// The WHERE clause carries the use check rather than the caller comparing
// first: two enrolments redeeming the last use of a key concurrently would
// both pass a read-then-write check, and a single-use key would be spent
// twice. Letting the UPDATE decide is what makes "single use" mean it.
func (s *Store) consumeSetupKey(ctx context.Context, tx *sql.Tx, key *SetupKey, now time.Time) error {
	query := s.db.Rebind(`
		UPDATE setup_keys
		SET uses = uses + 1
		WHERE id = ?
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > ?)
		  AND (max_uses = 0 OR uses < max_uses)`)

	result, err := tx.ExecContext(ctx, query, key.ID, now)
	if err != nil {
		return fmt.Errorf("consuming the setup key: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("consuming the setup key: %w", err)
	}
	if affected == 0 {
		// Re-read to report *why*, for the log. The caller still tells the
		// enroller only that the key was rejected.
		fresh, readErr := s.setupKeyByIDTx(ctx, tx, key.ID)
		if readErr != nil {
			return readErr
		}
		if reason := fresh.Redeemable(now); reason != nil {
			return reason
		}
		// It reads as redeemable now, so another transaction took the last use
		// between our read and this update — which is exactly the race the
		// conditional UPDATE exists to lose safely.
		return ErrKeyExhausted
	}
	key.Uses++
	return nil
}

func (s *Store) setupKeyByIDTx(ctx context.Context, tx *sql.Tx, id string) (*SetupKey, error) {
	return s.oneSetupKey(ctx, tx, `SELECT `+setupKeyColumns+` FROM setup_keys WHERE id = ?`, id)
}

func (s *Store) oneSetupKey(ctx context.Context, q Queryer, query string, args ...any) (*SetupKey, error) {
	rows, err := q.QueryContext(ctx, s.db.Rebind(query), args...)
	if err != nil {
		return nil, fmt.Errorf("reading the setup key: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("reading the setup key: %w", err)
		}
		return nil, ErrKeyNotFound
	}
	return scanSetupKey(rows)
}

// rowScanner is satisfied by *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanSetupKey(row rowScanner) (*SetupKey, error) {
	var (
		key       SetupKey
		tags      string
		expiresAt sql.NullTime
		revokedAt sql.NullTime
	)
	err := row.Scan(&key.ID, &key.keyHash, &key.DisplayHint, &key.Description, &tags,
		&key.CreatedBy, &key.CreatedAt, &expiresAt, &key.MaxUses, &key.Uses, &revokedAt)
	if err != nil {
		return nil, fmt.Errorf("reading the setup key: %w", err)
	}

	key.Tags = splitTags(tags)
	if expiresAt.Valid {
		t := expiresAt.Time
		key.ExpiresAt = &t
	}
	if revokedAt.Valid {
		t := revokedAt.Time
		key.RevokedAt = &t
	}
	return &key, nil
}

// normaliseTags trims, lower-cases and de-duplicates, dropping empties, so that
// "Servers", "servers " and "servers" are one tag rather than three.
func normaliseTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" {
			continue
		}
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

func splitTags(joined string) []string {
	if joined == "" {
		return nil
	}
	return normaliseTags(strings.Split(joined, ","))
}

func truncate(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}
