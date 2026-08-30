package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/CauaMora1s/Headnet/internal/storage"
	"github.com/CauaMora1s/Headnet/packages/shared"
)

// IDPrefix marks a user identifier, so an ID is self-describing wherever it
// appears in a log or an audit record.
const IDPrefix = "usr"

// Role determines what a user may do.
//
// Only two exist deliberately. A finer-grained permission model belongs with
// the policy engine in Phase 6, not bolted on here; inventing roles before
// there is anything to authorise produces a model that fits nothing.
type Role string

const (
	// RoleAdmin may manage users, devices and policy.
	RoleAdmin Role = "admin"
	// RoleMember may enrol and manage their own devices.
	RoleMember Role = "member"
)

// Valid reports whether the role is one this build understands.
func (r Role) Valid() bool { return r == RoleAdmin || r == RoleMember }

// Provider names where a user's identity comes from.
type Provider string

const (
	// ProviderLocal is an email address and a locally-stored password hash.
	ProviderLocal Provider = "local"
	// ProviderOIDC is an external identity provider.
	ProviderOIDC Provider = "oidc"
)

// Valid reports whether the provider is one this build understands.
func (p Provider) Valid() bool { return p == ProviderLocal || p == ProviderOIDC }

// Errors callers are expected to branch on.
var (
	// ErrUserNotFound means no account matched. Handlers must be careful not
	// to turn this into a distinguishable response on the login path, or the
	// endpoint becomes an oracle for which addresses are registered.
	ErrUserNotFound = errors.New("no such user")

	// ErrEmailTaken means the address is already registered.
	ErrEmailTaken = errors.New("that email address is already registered")

	// ErrInvalidEmail means the address could not be parsed.
	ErrInvalidEmail = errors.New("that is not a valid email address")

	// ErrNoPassword means the account has no local password, because it is
	// authenticated by an external provider.
	ErrNoPassword = errors.New("the account has no local password")
)

// User is an account.
//
// The password hash is deliberately unexported. It cannot be reached by a
// JSON encoder, a template, or a struct-printing log call, so the only way it
// leaves this package is through a method that was written on purpose.
type User struct {
	ID          string
	Email       string
	EmailNorm   string
	DisplayName string
	Role        Role
	Provider    Provider
	CreatedAt   time.Time
	UpdatedAt   time.Time
	// LastLoginAt is nil until the account first signs in.
	LastLoginAt *time.Time

	passwordHash string
}

// String renders the account without its password hash.
//
// This is not cosmetic. Making the field unexported keeps it away from a JSON
// encoder and from a template, but fmt reaches unexported fields quite
// happily: without this method, a single logger.Info("...", "user", u) or a
// %+v in an error message would put an Argon2id hash into the logs. The
// receiver is a value so that both User and *User are covered.
func (u User) String() string {
	return fmt.Sprintf("User{ID:%s Email:%s Role:%s Provider:%s}",
		u.ID, u.EmailNorm, u.Role, u.Provider)
}

// GoString covers the %#v verb for the same reason as String.
func (u User) GoString() string { return u.String() }

// IsAdmin reports whether the user may perform administrative actions.
func (u *User) IsAdmin() bool { return u.Role == RoleAdmin }

// HasPassword reports whether the account can be authenticated locally.
func (u *User) HasPassword() bool { return u.passwordHash != "" }

// VerifyPassword checks a password against this account.
//
// needsRehash reports that the stored hash used weaker parameters than are
// current and should be replaced; see UserStore.UpdatePassword.
func (u *User) VerifyPassword(password string) (ok bool, needsRehash bool, err error) {
	if !u.HasPassword() {
		return false, false, ErrNoPassword
	}
	return VerifyPassword(password, u.passwordHash)
}

// NormalizeEmail lower-cases and trims an address for comparison and storage
// in email_norm.
//
// Only the case of the whole address is folded. Headnet does not strip dots or
// plus-suffixes: those are provider-specific conventions, not part of the
// standard, and treating ada+vpn@example.com as the same account as
// ada@example.com would be wrong for most mail systems.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ValidateEmail parses an address and returns its normalised form.
func ValidateEmail(email string) (string, error) {
	trimmed := strings.TrimSpace(email)
	if trimmed == "" {
		return "", fmt.Errorf("%w: it is empty", ErrInvalidEmail)
	}
	// An address longer than this cannot be delivered anyway, and the bound
	// keeps a pathological input away from the parser.
	if len(trimmed) > 320 {
		return "", fmt.Errorf("%w: it is too long", ErrInvalidEmail)
	}

	addr, err := mail.ParseAddress(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidEmail, trimmed)
	}
	// ParseAddress accepts a display name, as in `Ada <ada@example.com>`.
	// Accepting that here would store something that is not an address.
	if addr.Address != trimmed {
		return "", fmt.Errorf("%w: give the address on its own, without a name", ErrInvalidEmail)
	}
	return NormalizeEmail(addr.Address), nil
}

// NewUser describes an account to create.
type NewUser struct {
	// Email as the user typed it.
	Email string
	// Password in plaintext. It is hashed before it reaches the database and
	// is never stored or logged.
	Password string
	// DisplayName is optional.
	DisplayName string
	// Role defaults to RoleMember when empty.
	Role Role
}

// StoreOptions configure a UserStore.
type StoreOptions struct {
	// Params are the Argon2id cost settings. The zero value means
	// DefaultParams.
	Params Params
	// MinPasswordLength is raised to MinPasswordLength if lower.
	MinPasswordLength int
}

// UserStore reads and writes accounts.
type UserStore struct {
	db                *storage.DB
	params            Params
	minPasswordLength int
}

// NewUserStore builds a store over an open database.
func NewUserStore(db *storage.DB, opts StoreOptions) *UserStore {
	params := opts.Params
	if params == (Params{}) {
		params = DefaultParams()
	}
	minLength := opts.MinPasswordLength
	if minLength < MinPasswordLength {
		minLength = MinPasswordLength
	}
	return &UserStore{db: db, params: params, minPasswordLength: minLength}
}

const userColumns = `id, email, email_norm, display_name, password_hash, role, provider,
	created_at, updated_at, last_login_at`

// Create inserts a local account inside the caller's transaction.
//
// A transaction is required because creating a user is rarely the whole
// operation — first-run bootstrap also has to claim the installation, and both
// must succeed or neither.
func (s *UserStore) Create(ctx context.Context, tx *sql.Tx, in NewUser, now time.Time) (*User, error) {
	if tx == nil {
		return nil, errors.New("auth: creating a user requires a transaction")
	}

	normalized, err := ValidateEmail(in.Email)
	if err != nil {
		return nil, err
	}
	if err := CheckPasswordLength(in.Password, s.minPasswordLength); err != nil {
		return nil, err
	}

	role := in.Role
	if role == "" {
		role = RoleMember
	}
	if !role.Valid() {
		return nil, fmt.Errorf("auth: %q is not a valid role", role)
	}

	hash, err := HashPassword(in.Password, s.params)
	if err != nil {
		return nil, err
	}

	user := &User{
		ID:           shared.NewID(IDPrefix),
		Email:        strings.TrimSpace(in.Email),
		EmailNorm:    normalized,
		DisplayName:  strings.TrimSpace(in.DisplayName),
		Role:         role,
		Provider:     ProviderLocal,
		CreatedAt:    now,
		UpdatedAt:    now,
		passwordHash: hash,
	}

	_, err = tx.ExecContext(ctx, s.db.Rebind(`
		INSERT INTO users (`+userColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		user.ID, user.Email, user.EmailNorm, user.DisplayName, user.passwordHash,
		string(user.Role), string(user.Provider), user.CreatedAt, user.UpdatedAt, nil,
	)
	if err != nil {
		// Let the unique index decide rather than checking first, which would
		// race with a concurrent registration of the same address.
		if storage.IsUniqueViolation(err) {
			return nil, fmt.Errorf("%w: %s", ErrEmailTaken, normalized)
		}
		return nil, fmt.Errorf("creating the user: %w", err)
	}
	return user, nil
}

// ByEmail looks an account up by address, case-insensitively.
func (s *UserStore) ByEmail(ctx context.Context, email string) (*User, error) {
	return s.one(ctx, s.db, `SELECT `+userColumns+` FROM users WHERE email_norm = ?`, NormalizeEmail(email))
}

// ByID looks an account up by identifier.
func (s *UserStore) ByID(ctx context.Context, id string) (*User, error) {
	return s.one(ctx, s.db, `SELECT `+userColumns+` FROM users WHERE id = ?`, id)
}

// Count returns how many accounts exist. First-run bootstrap uses it to decide
// whether the installation has been claimed.
func (s *UserStore) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting users: %w", err)
	}
	return n, nil
}

// UpdatePassword replaces an account's password, rehashing at current cost.
func (s *UserStore) UpdatePassword(ctx context.Context, id, password string, now time.Time) error {
	if err := CheckPasswordLength(password, s.minPasswordLength); err != nil {
		return err
	}
	hash, err := HashPassword(password, s.params)
	if err != nil {
		return err
	}
	return s.setPasswordHash(ctx, id, hash, now)
}

// RehashPassword re-derives the stored hash at current cost after a successful
// login reported needsRehash. The password is already known to be correct, so
// this is an upgrade rather than a change.
func (s *UserStore) RehashPassword(ctx context.Context, id, password string, now time.Time) error {
	hash, err := HashPassword(password, s.params)
	if err != nil {
		return err
	}
	return s.setPasswordHash(ctx, id, hash, now)
}

func (s *UserStore) setPasswordHash(ctx context.Context, id, hash string, now time.Time) error {
	result, err := s.db.ExecContext(ctx,
		s.db.Rebind(`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`), hash, now, id)
	if err != nil {
		return fmt.Errorf("updating the password: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("updating the password: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: %s", ErrUserNotFound, id)
	}
	return nil
}

// RecordLogin stamps a successful sign-in.
func (s *UserStore) RecordLogin(ctx context.Context, id string, now time.Time) error {
	_, err := s.db.ExecContext(ctx,
		s.db.Rebind(`UPDATE users SET last_login_at = ? WHERE id = ?`), now, id)
	if err != nil {
		return fmt.Errorf("recording the login: %w", err)
	}
	return nil
}

// rowQuerier is the single-row read shared by *sql.DB and *sql.Tx.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *UserStore) one(ctx context.Context, q rowQuerier, query string, args ...any) (*User, error) {
	var (
		user         User
		passwordHash sql.NullString
		lastLogin    sql.NullTime
		role         string
		provider     string
	)

	err := q.QueryRowContext(ctx, s.db.Rebind(query), args...).Scan(
		&user.ID, &user.Email, &user.EmailNorm, &user.DisplayName, &passwordHash,
		&role, &provider, &user.CreatedAt, &user.UpdatedAt, &lastLogin,
	)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrUserNotFound
	case err != nil:
		return nil, fmt.Errorf("reading the user: %w", err)
	}

	user.Role = Role(role)
	user.Provider = Provider(provider)
	user.passwordHash = passwordHash.String
	if lastLogin.Valid {
		t := lastLogin.Time
		user.LastLoginAt = &t
	}
	return &user, nil
}
