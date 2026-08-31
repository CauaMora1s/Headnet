package devices_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/CauaMora1s/Headnet/internal/auth"
	"github.com/CauaMora1s/Headnet/internal/config"
	"github.com/CauaMora1s/Headnet/internal/devices"
	"github.com/CauaMora1s/Headnet/internal/network"
	"github.com/CauaMora1s/Headnet/internal/storage"
	"github.com/CauaMora1s/Headnet/internal/storage/storagetest"
)

const (
	testPassword = "correct horse battery staple"
	// A tiny pool, so exhaustion is reachable in a test.
	testIPv4Pool = "100.100.0.0/24"
)

// fixture is a migrated database with a store and one admin account.
type fixture struct {
	store *devices.Store
	db    *storage.DB
	admin *auth.User
	users *auth.UserStore
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureWithPools(t, testIPv4Pool, "")
}

func newFixtureWithPools(t *testing.T, v4, v6 string) *fixture {
	t.Helper()

	db := storagetest.Open(t)

	netCfg := config.Default().Network
	netCfg.IPv4CIDR = v4
	netCfg.IPv6CIDR = v6

	allocator, err := network.NewAllocator(db, netCfg)
	if err != nil {
		t.Fatalf("NewAllocator failed: %v", err)
	}
	store, err := devices.NewStore(db, allocator)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	users := auth.NewUserStore(db, auth.StoreOptions{
		// Cheap hashing: these tests are not about Argon2 cost.
		Params: auth.Params{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32},
	})

	var admin *auth.User
	err = db.InTx(t.Context(), func(tx *sql.Tx) error {
		var err error
		admin, err = users.Create(t.Context(), tx, auth.NewUser{
			Email: "admin@example.com", Password: testPassword, Role: auth.RoleAdmin,
		}, time.Now().UTC())
		return err
	})
	if err != nil {
		t.Fatalf("seeding the admin failed: %v", err)
	}

	return &fixture{store: store, db: db, admin: admin, users: users}
}

// newUser adds another account, for the authorisation tests.
func (f *fixture) newUser(t *testing.T, email string, role auth.Role) *auth.User {
	t.Helper()
	var user *auth.User
	err := f.db.InTx(t.Context(), func(tx *sql.Tx) error {
		var err error
		user, err = f.users.Create(t.Context(), tx,
			auth.NewUser{Email: email, Password: testPassword, Role: role}, time.Now().UTC())
		return err
	})
	if err != nil {
		t.Fatalf("creating %s failed: %v", email, err)
	}
	return user
}

// createKey issues a setup key.
func (f *fixture) createKey(t *testing.T, in devices.NewSetupKey) *devices.IssuedSetupKey {
	t.Helper()
	issued, err := f.store.CreateSetupKey(t.Context(), f.admin.ID, in, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateSetupKey failed: %v", err)
	}
	return issued
}

func TestCreateSetupKeyReturnsTheTokenOnce(t *testing.T) {
	f := newFixture(t)
	issued := f.createKey(t, devices.NewSetupKey{Description: "build agents"})

	if issued.Token == "" {
		t.Fatal("no token was returned")
	}
	if !strings.HasPrefix(issued.Key.ID, devices.SetupKeyIDPrefix+"_") {
		t.Errorf("ID = %q, want a %q prefix", issued.Key.ID, devices.SetupKeyIDPrefix)
	}
	if issued.Key.Description != "build agents" {
		t.Errorf("Description = %q", issued.Key.Description)
	}

	// The hint identifies the key without being able to spend it.
	if !strings.HasPrefix(issued.Token, issued.Key.DisplayHint) {
		t.Errorf("DisplayHint %q is not the start of the token", issued.Key.DisplayHint)
	}
	if len(issued.Key.DisplayHint) >= len(issued.Token) {
		t.Fatal("the display hint is the whole token; it would be a usable credential")
	}
}

func TestTheKeyIsNeverStored(t *testing.T) {
	// The property that makes a stolen database not a set of working
	// enrolment credentials.
	f := newFixture(t)
	issued := f.createKey(t, devices.NewSetupKey{})

	rows, err := f.db.QueryContext(t.Context(),
		`SELECT id, key_hash, display_hint, description FROM setup_keys`)
	if err != nil {
		t.Fatalf("reading setup_keys failed: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, hash, hint, description string
		if err := rows.Scan(&id, &hash, &hint, &description); err != nil {
			t.Fatalf("scanning failed: %v", err)
		}
		if strings.Contains(strings.Join([]string{id, hash, hint, description}, "|"), issued.Token) {
			t.Fatal("the setup_keys table contains the raw key")
		}
		if len(hash) != 64 {
			t.Fatalf("key_hash = %q, want a hex SHA-256 digest", hash)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading setup_keys failed: %v", err)
	}
}

func TestTheDisplayHintCannotEnrol(t *testing.T) {
	// A list of keys must not itself be a source of usable keys.
	f := newFixture(t)
	issued := f.createKey(t, devices.NewSetupKey{})

	if _, err := f.store.SetupKeyByToken(t.Context(), nil, issued.Key.DisplayHint); !errors.Is(err, devices.ErrKeyNotFound) {
		t.Fatalf("looking a key up by its display hint returned %v, want ErrKeyNotFound", err)
	}
}

func TestTheKeyHashCannotEscapeTheStruct(t *testing.T) {
	// Same reasoning as auth.User and auth.Session: unexported keeps it from a
	// JSON encoder, String and GoString keep it out of %+v.
	f := newFixture(t)
	issued := f.createKey(t, devices.NewSetupKey{})

	encoded, err := json.Marshal(issued.Key)
	if err != nil {
		t.Fatalf("marshalling failed: %v", err)
	}
	for _, rendered := range []string{
		string(encoded),
		fmt.Sprintf("%v", issued.Key),
		fmt.Sprintf("%+v", issued.Key),
		fmt.Sprintf("%#v", issued.Key),
	} {
		if strings.Contains(strings.ToLower(rendered), "keyhash") {
			t.Fatalf("the key hash appeared in rendered output:\n%s", rendered)
		}
	}
	if !strings.Contains(fmt.Sprintf("%v", issued.Key), issued.Key.ID) {
		t.Error("the redacted form dropped the ID, making it useless in a log")
	}
}

func TestExpiryIsTheDefault(t *testing.T) {
	// A key that never expires is one you will still be finding in a shell
	// history years later, so it has to be asked for.
	f := newFixture(t)
	issued := f.createKey(t, devices.NewSetupKey{})

	if issued.Key.ExpiresAt == nil {
		t.Fatal("a key created without an expiry got none")
	}
	want := issued.Key.CreatedAt.Add(devices.DefaultExpiry)
	if !issued.Key.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want the default %v", issued.Key.ExpiresAt, want)
	}
}

func TestNeverExpiringRequiresAnExplicitChoice(t *testing.T) {
	f := newFixture(t)

	issued := f.createKey(t, devices.NewSetupKey{NeverExpires: true})
	if issued.Key.ExpiresAt != nil {
		t.Fatalf("ExpiresAt = %v, want nil when NeverExpires is set", issued.Key.ExpiresAt)
	}
	if issued.Key.Expired(time.Now().Add(100 * 365 * 24 * time.Hour)) {
		t.Error("a never-expiring key reported itself expired")
	}
}

func TestCustomExpiryIsHonoured(t *testing.T) {
	f := newFixture(t)
	issued := f.createKey(t, devices.NewSetupKey{ExpiresIn: time.Hour})

	want := issued.Key.CreatedAt.Add(time.Hour)
	if !issued.Key.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", issued.Key.ExpiresAt, want)
	}
}

func TestRedeemableReportsTheReason(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	past := now.Add(-time.Hour)

	live := &devices.SetupKey{MaxUses: 0}
	if err := live.Redeemable(now); err != nil {
		t.Errorf("a live key reported %v", err)
	}

	revoked := &devices.SetupKey{RevokedAt: &past}
	if err := revoked.Redeemable(now); !errors.Is(err, devices.ErrKeyRevoked) {
		t.Errorf("revoked key = %v, want ErrKeyRevoked", err)
	}

	expired := &devices.SetupKey{ExpiresAt: &past}
	if err := expired.Redeemable(now); !errors.Is(err, devices.ErrKeyExpired) {
		t.Errorf("expired key = %v, want ErrKeyExpired", err)
	}

	exhausted := &devices.SetupKey{MaxUses: 1, Uses: 1}
	if err := exhausted.Redeemable(now); !errors.Is(err, devices.ErrKeyExhausted) {
		t.Errorf("exhausted key = %v, want ErrKeyExhausted", err)
	}

	// Revocation is named first: it is a deliberate act and the most useful
	// thing to tell an operator.
	both := &devices.SetupKey{RevokedAt: &past, ExpiresAt: &past}
	if err := both.Redeemable(now); !errors.Is(err, devices.ErrKeyRevoked) {
		t.Errorf("revoked and expired = %v, want ErrKeyRevoked first", err)
	}
}

func TestUnlimitedKeysAreNeverExhausted(t *testing.T) {
	t.Parallel()
	key := &devices.SetupKey{MaxUses: 0, Uses: 10_000}
	if key.Exhausted() {
		t.Fatal("a key with max_uses = 0 reported itself exhausted")
	}
	if key.SingleUse() {
		t.Fatal("a key with max_uses = 0 reported itself single-use")
	}
}

func TestRevokeSetupKey(t *testing.T) {
	f := newFixture(t)
	issued := f.createKey(t, devices.NewSetupKey{})
	now := time.Now().UTC()

	if err := f.store.RevokeSetupKey(t.Context(), issued.Key.ID, now); err != nil {
		t.Fatalf("RevokeSetupKey failed: %v", err)
	}

	reloaded, err := f.store.SetupKeyByID(t.Context(), issued.Key.ID)
	if err != nil {
		t.Fatalf("SetupKeyByID failed: %v", err)
	}
	if !reloaded.Revoked() {
		t.Fatal("the key is not marked revoked")
	}
	if !errors.Is(reloaded.Redeemable(now), devices.ErrKeyRevoked) {
		t.Fatal("a revoked key is still redeemable")
	}
}

func TestRevokingTwiceKeepsTheFirstTime(t *testing.T) {
	// The recorded time should be when the key actually stopped working, not
	// when someone last clicked the button.
	f := newFixture(t)
	issued := f.createKey(t, devices.NewSetupKey{})

	first := time.Now().UTC().Truncate(time.Second)
	if err := f.store.RevokeSetupKey(t.Context(), issued.Key.ID, first); err != nil {
		t.Fatalf("RevokeSetupKey failed: %v", err)
	}
	if err := f.store.RevokeSetupKey(t.Context(), issued.Key.ID, first.Add(time.Hour)); err != nil {
		t.Fatalf("the second RevokeSetupKey failed: %v", err)
	}

	reloaded, err := f.store.SetupKeyByID(t.Context(), issued.Key.ID)
	if err != nil {
		t.Fatalf("SetupKeyByID failed: %v", err)
	}
	if !reloaded.RevokedAt.UTC().Truncate(time.Second).Equal(first) {
		t.Fatalf("RevokedAt = %v, want the first revocation at %v", reloaded.RevokedAt, first)
	}
}

func TestRevokingAnUnknownKey(t *testing.T) {
	f := newFixture(t)
	err := f.store.RevokeSetupKey(t.Context(), "sk_NOSUCHTHING", time.Now().UTC())
	if !errors.Is(err, devices.ErrKeyNotFound) {
		t.Fatalf("error = %v, want ErrKeyNotFound", err)
	}
}

func TestListSetupKeys(t *testing.T) {
	f := newFixture(t)

	if keys, err := f.store.ListSetupKeys(t.Context()); err != nil || len(keys) != 0 {
		t.Fatalf("ListSetupKeys on an empty database = (%d keys, %v)", len(keys), err)
	}

	for i := range 3 {
		f.createKey(t, devices.NewSetupKey{Description: fmt.Sprintf("key %d", i)})
	}

	keys, err := f.store.ListSetupKeys(t.Context())
	if err != nil {
		t.Fatalf("ListSetupKeys failed: %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("listed %d keys, want 3", len(keys))
	}
	// Listing must never expose anything spendable.
	for _, key := range keys {
		if strings.Contains(fmt.Sprintf("%+v", key), "keyHash") {
			t.Fatal("a listed key rendered its hash")
		}
	}
}

func TestTagsAreNormalised(t *testing.T) {
	// "Servers", "servers " and "servers" must be one tag, or policy written
	// against them in Phase 6 would silently miss devices.
	f := newFixture(t)
	issued := f.createKey(t, devices.NewSetupKey{
		Tags: []string{"Servers", "servers ", " SERVERS", "build", "", "   "},
	})

	if len(issued.Key.Tags) != 2 {
		t.Fatalf("Tags = %v, want them de-duplicated and trimmed to two", issued.Key.Tags)
	}
	if issued.Key.Tags[0] != "servers" || issued.Key.Tags[1] != "build" {
		t.Fatalf("Tags = %v, want [servers build]", issued.Key.Tags)
	}

	reloaded, err := f.store.SetupKeyByID(t.Context(), issued.Key.ID)
	if err != nil {
		t.Fatalf("SetupKeyByID failed: %v", err)
	}
	if len(reloaded.Tags) != 2 {
		t.Fatalf("Tags after a round trip = %v", reloaded.Tags)
	}
}

func TestCreateSetupKeyValidatesItsInput(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()

	if _, err := f.store.CreateSetupKey(t.Context(), "", devices.NewSetupKey{}, now); err == nil {
		t.Error("CreateSetupKey accepted an empty creator")
	}
	if _, err := f.store.CreateSetupKey(t.Context(), f.admin.ID,
		devices.NewSetupKey{MaxUses: -1}, now); err == nil {
		t.Error("CreateSetupKey accepted a negative use limit")
	}
	if _, err := f.store.CreateSetupKey(t.Context(), f.admin.ID,
		devices.NewSetupKey{ExpiresIn: -time.Hour}, now); err == nil {
		t.Error("CreateSetupKey accepted a negative expiry")
	}
}

func TestDescriptionIsBounded(t *testing.T) {
	// Operator-supplied free text is still attacker-shaped input when the
	// operator's session is the thing being abused.
	f := newFixture(t)
	issued := f.createKey(t, devices.NewSetupKey{Description: strings.Repeat("x", 10_000)})

	if len([]rune(issued.Key.Description)) > 200 {
		t.Fatalf("the stored description is %d characters, want it truncated",
			len([]rune(issued.Key.Description)))
	}
}

func TestEveryKeyIsDistinct(t *testing.T) {
	f := newFixture(t)
	seen := make(map[string]struct{}, 20)
	for range 20 {
		issued := f.createKey(t, devices.NewSetupKey{})
		if _, dup := seen[issued.Token]; dup {
			t.Fatal("two setup keys were identical")
		}
		seen[issued.Token] = struct{}{}
	}
}

func TestDeletingTheCreatorDeletesTheirKeys(t *testing.T) {
	// ON DELETE CASCADE. A key outliving the account that issued it would be a
	// live enrolment credential belonging to nobody.
	f := newFixture(t)
	creator := f.newUser(t, "creator@example.com", auth.RoleAdmin)

	issued, err := f.store.CreateSetupKey(t.Context(), creator.ID, devices.NewSetupKey{}, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateSetupKey failed: %v", err)
	}

	if _, err := f.db.ExecContext(t.Context(),
		f.db.Rebind(`DELETE FROM users WHERE id = ?`), creator.ID); err != nil {
		t.Fatalf("deleting the creator failed: %v", err)
	}

	if _, err := f.store.SetupKeyByID(t.Context(), issued.Key.ID); !errors.Is(err, devices.ErrKeyNotFound) {
		t.Fatalf("error = %v, want the key to have been cascaded away", err)
	}
}

func TestNewStoreRequiresItsDependencies(t *testing.T) {
	db := storagetest.Open(t)
	allocator, err := network.NewAllocator(db, config.Default().Network)
	if err != nil {
		t.Fatalf("NewAllocator failed: %v", err)
	}

	if _, err := devices.NewStore(nil, allocator); err == nil {
		t.Error("NewStore accepted a nil database")
	}
	// A device without an address is not on the network, so an optional
	// allocator would allow one to be created.
	if _, err := devices.NewStore(db, nil); err == nil {
		t.Error("NewStore accepted a nil allocator")
	}
}
