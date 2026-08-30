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

// newStore returns a user store over a migrated database, using cheap hashing
// parameters so the suite stays fast.
func newStore(t *testing.T) (*auth.UserStore, *storage.DB) {
	t.Helper()
	db := storagetest.Open(t)
	return auth.NewUserStore(db, auth.StoreOptions{Params: cheapParams()}), db
}

// create inserts a user in its own committed transaction.
func create(t *testing.T, s *auth.UserStore, db *storage.DB, in auth.NewUser) *auth.User {
	t.Helper()
	var user *auth.User
	err := db.InTx(t.Context(), func(tx *sql.Tx) error {
		var err error
		user, err = s.Create(t.Context(), tx, in, time.Now().UTC())
		return err
	})
	if err != nil {
		t.Fatalf("creating %s failed: %v", in.Email, err)
	}
	return user
}

func TestCreateAndFetch(t *testing.T) {
	s, db := newStore(t)

	created := create(t, s, db, auth.NewUser{
		Email:       "Ada@Example.com",
		Password:    goodPassword,
		DisplayName: "Ada Lovelace",
		Role:        auth.RoleAdmin,
	})

	if !strings.HasPrefix(created.ID, auth.IDPrefix+"_") {
		t.Errorf("ID = %q, want a %q prefix", created.ID, auth.IDPrefix)
	}
	if created.Email != "Ada@Example.com" {
		t.Errorf("Email = %q, want the address as typed", created.Email)
	}
	if created.EmailNorm != "ada@example.com" {
		t.Errorf("EmailNorm = %q, want it lower-cased", created.EmailNorm)
	}
	if !created.IsAdmin() {
		t.Error("IsAdmin() = false for an admin")
	}
	if created.Provider != auth.ProviderLocal {
		t.Errorf("Provider = %q, want local", created.Provider)
	}
	if created.LastLoginAt != nil {
		t.Error("LastLoginAt is set on a brand-new account")
	}

	fetched, err := s.ByID(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("ByID failed: %v", err)
	}
	if fetched.Email != created.Email || fetched.Role != created.Role {
		t.Fatalf("ByID returned %+v, want it to match the created user", fetched)
	}
}

func TestLookupByEmailIsCaseInsensitive(t *testing.T) {
	// Otherwise Ada@example.com and ada@example.com are two accounts, which is
	// a confusing way to lose access to your own network.
	s, db := newStore(t)
	create(t, s, db, auth.NewUser{Email: "Ada@Example.com", Password: goodPassword})

	for _, variant := range []string{
		"ada@example.com", "Ada@Example.com", "ADA@EXAMPLE.COM", "  ada@example.com  ",
	} {
		if _, err := s.ByEmail(t.Context(), variant); err != nil {
			t.Errorf("ByEmail(%q) failed: %v", variant, err)
		}
	}
}

func TestDuplicateEmailIsRefused(t *testing.T) {
	s, db := newStore(t)
	create(t, s, db, auth.NewUser{Email: "ada@example.com", Password: goodPassword})

	// Including under a different case, which the unique index on the
	// normalised column is what actually prevents.
	for _, dup := range []string{"ada@example.com", "ADA@example.com"} {
		err := db.InTx(t.Context(), func(tx *sql.Tx) error {
			_, err := s.Create(t.Context(), tx, auth.NewUser{Email: dup, Password: goodPassword}, time.Now())
			return err
		})
		if !errors.Is(err, auth.ErrEmailTaken) {
			t.Fatalf("creating %q returned %v, want ErrEmailTaken", dup, err)
		}
	}
}

func TestConcurrentRegistrationOfOneAddressYieldsOneAccount(t *testing.T) {
	// Checking "does this email exist?" before inserting would race: two
	// requests both see nothing and both insert. The unique index decides, and
	// storage.IsUniqueViolation classifies the loser's error.
	s, db := newStore(t)

	const attempts = 8
	results := make(chan error, attempts)
	for range attempts {
		go func() {
			results <- db.InTx(t.Context(), func(tx *sql.Tx) error {
				_, err := s.Create(t.Context(), tx,
					auth.NewUser{Email: "race@example.com", Password: goodPassword}, time.Now().UTC())
				return err
			})
		}()
	}

	succeeded := 0
	for range attempts {
		switch err := <-results; {
		case err == nil:
			succeeded++
		case errors.Is(err, auth.ErrEmailTaken):
			// Expected for the losers.
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("%d of %d concurrent registrations succeeded, want exactly 1", succeeded, attempts)
	}

	count, err := s.Count(t.Context())
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("the database holds %d accounts, want 1", count)
	}
}

func TestPasswordVerification(t *testing.T) {
	s, db := newStore(t)
	user := create(t, s, db, auth.NewUser{Email: "ada@example.com", Password: goodPassword})

	ok, _, err := user.VerifyPassword(goodPassword)
	if err != nil || !ok {
		t.Fatalf("the correct password did not verify: ok=%v err=%v", ok, err)
	}

	ok, _, err = user.VerifyPassword("not the password")
	if err != nil {
		t.Fatalf("verification errored: %v", err)
	}
	if ok {
		t.Fatal("the wrong password verified")
	}
}

func TestThePlaintextPasswordIsNeverStored(t *testing.T) {
	// The single most important property of this table.
	s, db := newStore(t)
	create(t, s, db, auth.NewUser{Email: "ada@example.com", Password: goodPassword})

	rows, err := db.QueryContext(t.Context(), `SELECT id, email, display_name, password_hash FROM users`)
	if err != nil {
		t.Fatalf("reading the users table failed: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, email, displayName string
		var hash sql.NullString
		if err := rows.Scan(&id, &email, &displayName, &hash); err != nil {
			t.Fatalf("scanning failed: %v", err)
		}
		row := strings.Join([]string{id, email, displayName, hash.String}, "|")
		if strings.Contains(row, goodPassword) {
			t.Fatalf("the users table contains the plaintext password:\n%s", row)
		}
		if !strings.HasPrefix(hash.String, "$argon2id$") {
			t.Fatalf("password_hash = %q, want an Argon2id PHC string", hash.String)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the users table failed: %v", err)
	}
}

func TestThePasswordHashCannotBeSerialised(t *testing.T) {
	// The hash lives in an unexported field so it cannot reach a JSON encoder,
	// a template, or a struct-printing log call by accident.
	s, db := newStore(t)
	user := create(t, s, db, auth.NewUser{Email: "ada@example.com", Password: goodPassword})

	encoded, err := json.Marshal(user)
	if err != nil {
		t.Fatalf("marshalling the user failed: %v", err)
	}
	if strings.Contains(string(encoded), "argon2") {
		t.Fatalf("the password hash was serialised into JSON:\n%s", encoded)
	}

	if formatted := fmt.Sprintf("%+v", *user); strings.Contains(formatted, "argon2") {
		t.Fatalf("the password hash appeared in a formatted struct:\n%s", formatted)
	}
}

func TestEmailValidation(t *testing.T) {
	t.Parallel()
	valid := []string{
		"ada@example.com",
		"ada.lovelace@example.co.uk",
		"ada+vpn@example.com",
		"a@b.co",
	}
	for _, email := range valid {
		if _, err := auth.ValidateEmail(email); err != nil {
			t.Errorf("ValidateEmail(%q) failed: %v", email, err)
		}
	}

	invalid := []string{
		"",
		"   ",
		"notanemail",
		"@example.com",
		"ada@",
		"Ada Lovelace <ada@example.com>", // a display name is not an address
		strings.Repeat("a", 320) + "@example.com",
	}
	for _, email := range invalid {
		if _, err := auth.ValidateEmail(email); !errors.Is(err, auth.ErrInvalidEmail) {
			t.Errorf("ValidateEmail(%q) = %v, want ErrInvalidEmail", email, err)
		}
	}
}

func TestNormalizeEmailDoesNotStripPlusOrDots(t *testing.T) {
	t.Parallel()
	// Those are provider-specific conventions, not part of the standard.
	// Treating ada+vpn@example.com as ada@example.com would be wrong for most
	// mail systems, and would silently merge two people's accounts.
	if got := auth.NormalizeEmail("Ada+VPN@Example.com"); got != "ada+vpn@example.com" {
		t.Fatalf("NormalizeEmail = %q, want only the case folded", got)
	}
	if got := auth.NormalizeEmail("a.b@example.com"); got != "a.b@example.com" {
		t.Fatalf("NormalizeEmail = %q, want dots preserved", got)
	}
}

func TestCreateRejectsBadInput(t *testing.T) {
	s, db := newStore(t)

	tests := []struct {
		name string
		in   auth.NewUser
		want error
	}{
		{"invalid email", auth.NewUser{Email: "nope", Password: goodPassword}, auth.ErrInvalidEmail},
		{"short password", auth.NewUser{Email: "a@b.co", Password: "short"}, auth.ErrPasswordTooShort},
		{"empty password", auth.NewUser{Email: "a@b.co", Password: ""}, auth.ErrPasswordTooShort},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := db.InTx(t.Context(), func(tx *sql.Tx) error {
				_, err := s.Create(t.Context(), tx, tt.in, time.Now())
				return err
			})
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want it to wrap %v", err, tt.want)
			}
		})
	}
}

func TestCreateRejectsAnUnknownRole(t *testing.T) {
	s, db := newStore(t)
	err := db.InTx(t.Context(), func(tx *sql.Tx) error {
		_, err := s.Create(t.Context(), tx,
			auth.NewUser{Email: "a@b.co", Password: goodPassword, Role: "superuser"}, time.Now())
		return err
	})
	if err == nil {
		t.Fatal("Create accepted an unknown role")
	}
}

func TestCreateDefaultsToMember(t *testing.T) {
	s, db := newStore(t)
	user := create(t, s, db, auth.NewUser{Email: "ada@example.com", Password: goodPassword})
	if user.Role != auth.RoleMember {
		t.Fatalf("Role = %q, want member by default rather than admin", user.Role)
	}
	if user.IsAdmin() {
		t.Fatal("a user created without a role is an admin; the default must be the restrictive one")
	}
}

func TestCreateRequiresATransaction(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.Create(t.Context(), nil, auth.NewUser{Email: "a@b.co", Password: goodPassword}, time.Now()); err == nil {
		t.Fatal("Create accepted a nil transaction")
	}
}

func TestARolledBackCreateLeavesNoAccount(t *testing.T) {
	s, db := newStore(t)

	sentinel := errors.New("the surrounding operation failed")
	err := db.InTx(t.Context(), func(tx *sql.Tx) error {
		if _, err := s.Create(t.Context(), tx,
			auth.NewUser{Email: "ada@example.com", Password: goodPassword}, time.Now().UTC()); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("InTx error = %v, want the callback error", err)
	}

	if _, err := s.ByEmail(t.Context(), "ada@example.com"); !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("the rolled-back account survived (err=%v)", err)
	}
}

func TestUnknownUserLookups(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.ByEmail(t.Context(), "nobody@example.com"); !errors.Is(err, auth.ErrUserNotFound) {
		t.Errorf("ByEmail error = %v, want ErrUserNotFound", err)
	}
	if _, err := s.ByID(t.Context(), "usr_NOSUCHTHING"); !errors.Is(err, auth.ErrUserNotFound) {
		t.Errorf("ByID error = %v, want ErrUserNotFound", err)
	}
}

func TestCount(t *testing.T) {
	s, db := newStore(t)

	if n, err := s.Count(t.Context()); err != nil || n != 0 {
		t.Fatalf("Count on an empty database = (%d, %v), want (0, nil)", n, err)
	}
	create(t, s, db, auth.NewUser{Email: "one@example.com", Password: goodPassword})
	create(t, s, db, auth.NewUser{Email: "two@example.com", Password: goodPassword})
	if n, err := s.Count(t.Context()); err != nil || n != 2 {
		t.Fatalf("Count = (%d, %v), want (2, nil)", n, err)
	}
}

func TestUpdatePassword(t *testing.T) {
	s, db := newStore(t)
	user := create(t, s, db, auth.NewUser{Email: "ada@example.com", Password: goodPassword})

	const replacement = "a different long passphrase"
	if err := s.UpdatePassword(t.Context(), user.ID, replacement, time.Now().UTC()); err != nil {
		t.Fatalf("UpdatePassword failed: %v", err)
	}

	reloaded, err := s.ByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("ByID failed: %v", err)
	}
	if ok, _, _ := reloaded.VerifyPassword(replacement); !ok {
		t.Error("the new password does not verify")
	}
	if ok, _, _ := reloaded.VerifyPassword(goodPassword); ok {
		t.Error("the old password still verifies after a change")
	}
}

func TestUpdatePasswordEnforcesTheMinimumLength(t *testing.T) {
	s, db := newStore(t)
	user := create(t, s, db, auth.NewUser{Email: "ada@example.com", Password: goodPassword})

	if err := s.UpdatePassword(t.Context(), user.ID, "short", time.Now()); !errors.Is(err, auth.ErrPasswordTooShort) {
		t.Fatalf("error = %v, want ErrPasswordTooShort", err)
	}
}

func TestUpdatePasswordOnAnUnknownUser(t *testing.T) {
	s, _ := newStore(t)
	err := s.UpdatePassword(t.Context(), "usr_NOSUCHTHING", goodPassword, time.Now())
	if !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("error = %v, want ErrUserNotFound", err)
	}
}

func TestRehashUpgradesAWeakHashInPlace(t *testing.T) {
	// The upgrade path: a login with a correct password whose stored hash used
	// weaker parameters gets rehashed at current cost, without the user
	// noticing or having to change anything.
	db := storagetest.Open(t)
	weak := auth.NewUserStore(db, auth.StoreOptions{
		Params: auth.Params{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32},
	})
	user := create(t, weak, db, auth.NewUser{Email: "ada@example.com", Password: goodPassword})

	if _, needsRehash, _ := user.VerifyPassword(goodPassword); !needsRehash {
		t.Fatal("the weak hash was not flagged for rehashing")
	}

	strong := auth.NewUserStore(db, auth.StoreOptions{Params: auth.DefaultParams()})
	if err := strong.RehashPassword(t.Context(), user.ID, goodPassword, time.Now().UTC()); err != nil {
		t.Fatalf("RehashPassword failed: %v", err)
	}

	reloaded, err := strong.ByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("ByID failed: %v", err)
	}
	ok, needsRehash, err := reloaded.VerifyPassword(goodPassword)
	if err != nil || !ok {
		t.Fatalf("the password stopped verifying after a rehash: ok=%v err=%v", ok, err)
	}
	if needsRehash {
		t.Fatal("the hash is still flagged for rehashing after being upgraded")
	}
}

func TestRecordLogin(t *testing.T) {
	s, db := newStore(t)
	user := create(t, s, db, auth.NewUser{Email: "ada@example.com", Password: goodPassword})

	when := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	if err := s.RecordLogin(t.Context(), user.ID, when); err != nil {
		t.Fatalf("RecordLogin failed: %v", err)
	}

	reloaded, err := s.ByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("ByID failed: %v", err)
	}
	if reloaded.LastLoginAt == nil {
		t.Fatal("LastLoginAt is still nil after a recorded login")
	}
	if !reloaded.LastLoginAt.UTC().Equal(when) {
		t.Fatalf("LastLoginAt = %v, want %v", reloaded.LastLoginAt.UTC(), when)
	}
}

func TestRoleAndProviderValidity(t *testing.T) {
	t.Parallel()
	for _, r := range []auth.Role{auth.RoleAdmin, auth.RoleMember} {
		if !r.Valid() {
			t.Errorf("%q.Valid() = false", r)
		}
	}
	for _, r := range []auth.Role{"", "superuser", "ADMIN"} {
		if r.Valid() {
			t.Errorf("%q.Valid() = true", r)
		}
	}
	for _, p := range []auth.Provider{auth.ProviderLocal, auth.ProviderOIDC} {
		if !p.Valid() {
			t.Errorf("%q.Valid() = false", p)
		}
	}
	if auth.Provider("saml").Valid() {
		t.Error("an unsupported provider reported valid")
	}
}
