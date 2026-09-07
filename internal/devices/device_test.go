package devices_test

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CauaMora1s/Headnet/internal/auth"
	"github.com/CauaMora1s/Headnet/internal/devices"
	"github.com/CauaMora1s/Headnet/internal/network"
)

// publicKey returns a syntactically valid WireGuard public key.
func publicKey(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generating a key failed: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// register adds a device owned by the fixture's admin.
func (f *fixture) register(t *testing.T, name string) *devices.Device {
	t.Helper()
	device, err := f.store.Register(t.Context(), f.admin.ID, devices.NewDevice{
		Name: name, PublicKey: publicKey(t), OS: "linux", Hostname: name,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("registering %s failed: %v", name, err)
	}
	return device
}

func TestRegisterAllocatesAnAddress(t *testing.T) {
	f := newFixture(t)
	device := f.register(t, "laptop")

	if !strings.HasPrefix(device.ID, devices.DeviceIDPrefix+"_") {
		t.Errorf("ID = %q, want a %q prefix", device.ID, devices.DeviceIDPrefix)
	}
	if !device.IPv4.IsValid() {
		t.Fatal("no IPv4 address was allocated")
	}
	if device.IPv4.String() != "100.100.0.1" {
		t.Errorf("IPv4 = %s, want the lowest free address", device.IPv4)
	}
	if device.Revoked() {
		t.Error("a newly registered device is revoked")
	}
	if device.UserID != f.admin.ID {
		t.Errorf("UserID = %q, want the registering user", device.UserID)
	}
}

func TestRegisterAllocatesBothFamiliesWhenIPv6IsEnabled(t *testing.T) {
	f := newFixtureWithPools(t, testIPv4Pool, "fd7a:115c:a1e0::/48")
	device := f.register(t, "laptop")

	if len(device.Addrs()) != 2 {
		t.Fatalf("Addrs() = %v, want both families", device.Addrs())
	}
	if !device.IPv6.IsValid() {
		t.Fatal("no IPv6 address was allocated")
	}
}

func TestEachDeviceGetsADistinctAddress(t *testing.T) {
	f := newFixture(t)

	seen := make(map[string]string)
	for i := range 20 {
		device := f.register(t, "device-"+strconv.Itoa(i))
		addr := device.IPv4.String()
		if previous, dup := seen[addr]; dup {
			t.Fatalf("%s was given to both %s and %s", addr, previous, device.Name)
		}
		seen[addr] = device.Name
	}
}

func TestDuplicatePublicKeyIsRefused(t *testing.T) {
	f := newFixture(t)
	key := publicKey(t)
	now := time.Now().UTC()

	if _, err := f.store.Register(t.Context(), f.admin.ID,
		devices.NewDevice{Name: "first", PublicKey: key}, now); err != nil {
		t.Fatalf("the first registration failed: %v", err)
	}

	_, err := f.store.Register(t.Context(), f.admin.ID,
		devices.NewDevice{Name: "second", PublicKey: key}, now)
	if !errors.Is(err, devices.ErrPublicKeyTaken) {
		t.Fatalf("error = %v, want ErrPublicKeyTaken", err)
	}
}

func TestARejectedRegistrationLeaksNoAddress(t *testing.T) {
	// The device row and its allocation are one transaction. If a duplicate
	// key rolled back only the row, the pool would bleed an address on every
	// failed attempt.
	f := newFixture(t)
	key := publicKey(t)
	now := time.Now().UTC()

	if _, err := f.store.Register(t.Context(), f.admin.ID,
		devices.NewDevice{Name: "first", PublicKey: key}, now); err != nil {
		t.Fatalf("the first registration failed: %v", err)
	}

	for range 5 {
		if _, err := f.store.Register(t.Context(), f.admin.ID,
			devices.NewDevice{Name: "dup", PublicKey: key}, now); err == nil {
			t.Fatal("a duplicate registration succeeded")
		}
	}

	// The next good registration gets the very next address, proving nothing
	// was consumed by the failures.
	next := f.register(t, "second")
	if next.IPv4.String() != "100.100.0.2" {
		t.Fatalf("the next device got %s, want 100.100.0.2 — the failed attempts leaked addresses",
			next.IPv4)
	}
}

func TestPublicKeyValidation(t *testing.T) {
	t.Parallel()
	valid := base64.StdEncoding.EncodeToString(append([]byte{1}, make([]byte, 31)...))
	if err := devices.ValidatePublicKey(valid); err != nil {
		t.Errorf("a valid key was rejected: %v", err)
	}

	tests := []struct {
		name string
		key  string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"not base64", "!!!not base64!!!"},
		{"too short", base64.StdEncoding.EncodeToString(make([]byte, 16))},
		{"too long", base64.StdEncoding.EncodeToString(make([]byte, 64))},
		{"all zeroes", base64.StdEncoding.EncodeToString(make([]byte, 32))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := devices.ValidatePublicKey(tt.key); !errors.Is(err, devices.ErrInvalidPublicKey) {
				t.Fatalf("ValidatePublicKey(%q) = %v, want ErrInvalidPublicKey", tt.key, err)
			}
		})
	}
}

func TestAnAllZeroKeyIsRefused(t *testing.T) {
	// An uninitialised buffer looks exactly like this. Accepting it would mean
	// enrolling a device whose key generation silently failed.
	f := newFixture(t)
	_, err := f.store.Register(t.Context(), f.admin.ID, devices.NewDevice{
		Name: "broken", PublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32)),
	}, time.Now().UTC())
	if !errors.Is(err, devices.ErrInvalidPublicKey) {
		t.Fatalf("error = %v, want ErrInvalidPublicKey", err)
	}
}

func TestRegisterRequiresAName(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"", "   "} {
		_, err := f.store.Register(t.Context(), f.admin.ID,
			devices.NewDevice{Name: name, PublicKey: publicKey(t)}, time.Now().UTC())
		if !errors.Is(err, devices.ErrInvalidName) {
			t.Errorf("name %q gave %v, want ErrInvalidName", name, err)
		}
	}
}

func TestDeviceSuppliedStringsAreBounded(t *testing.T) {
	// A device is not trusted, so everything it reports is bounded.
	f := newFixture(t)
	device, err := f.store.Register(t.Context(), f.admin.ID, devices.NewDevice{
		Name:      strings.Repeat("n", 10_000),
		PublicKey: publicKey(t),
		OS:        strings.Repeat("o", 10_000),
		Hostname:  strings.Repeat("h", 10_000),
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if len(device.Name) > 64 || len(device.OS) > 128 || len(device.Hostname) > 128 {
		t.Fatalf("stored strings were not bounded: name=%d os=%d hostname=%d",
			len(device.Name), len(device.OS), len(device.Hostname))
	}
}

func TestRevokeReleasesTheAddress(t *testing.T) {
	f := newFixture(t)
	scope := devices.ScopeFor(f.admin.ID, true)

	first := f.register(t, "laptop")
	if err := f.store.Revoke(t.Context(), first.ID, scope, time.Now().UTC()); err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}

	reloaded, err := f.store.ByID(t.Context(), first.ID, scope)
	if err != nil {
		t.Fatalf("ByID failed: %v", err)
	}
	if !reloaded.Revoked() {
		t.Fatal("the device is not marked revoked")
	}
	if reloaded.IPv4.IsValid() {
		t.Errorf("the revoked device still shows an address: %s", reloaded.IPv4)
	}

	// The address returns to the pool.
	next := f.register(t, "replacement")
	if next.IPv4 != first.IPv4 {
		t.Fatalf("the replacement got %s, want the released %s", next.IPv4, first.IPv4)
	}
}

func TestARevokedKeyCanNeverBeRegisteredAgain(t *testing.T) {
	// The unique constraint is permanent on purpose: a revoked key that could
	// be registered again would not really be revoked.
	f := newFixture(t)
	key := publicKey(t)
	now := time.Now().UTC()

	device, err := f.store.Register(t.Context(), f.admin.ID,
		devices.NewDevice{Name: "laptop", PublicKey: key}, now)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if err := f.store.Revoke(t.Context(), device.ID, devices.ScopeFor(f.admin.ID, true), now); err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}

	_, err = f.store.Register(t.Context(), f.admin.ID,
		devices.NewDevice{Name: "same key again", PublicKey: key}, now)
	if !errors.Is(err, devices.ErrPublicKeyTaken) {
		t.Fatalf("error = %v, want a revoked key to stay blocked", err)
	}
}

func TestRevokingTwiceIsRefused(t *testing.T) {
	f := newFixture(t)
	scope := devices.ScopeFor(f.admin.ID, true)
	device := f.register(t, "laptop")
	now := time.Now().UTC()

	if err := f.store.Revoke(t.Context(), device.ID, scope, now); err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}
	if err := f.store.Revoke(t.Context(), device.ID, scope, now); !errors.Is(err, devices.ErrDeviceRevoked) {
		t.Fatalf("error = %v, want ErrDeviceRevoked", err)
	}
}

func TestAMemberSeesOnlyTheirOwnDevices(t *testing.T) {
	// The scope goes into the query rather than being checked in a handler,
	// so a future endpoint cannot forget to apply it.
	f := newFixture(t)
	member := f.newUser(t, "member@example.com", auth.RoleMember)
	now := time.Now().UTC()

	adminDevice := f.register(t, "admin-laptop")
	memberDevice, err := f.store.Register(t.Context(), member.ID,
		devices.NewDevice{Name: "member-laptop", PublicKey: publicKey(t)}, now)
	if err != nil {
		t.Fatalf("registering the member device failed: %v", err)
	}

	memberScope := devices.ScopeFor(member.ID, false)
	listed, err := f.store.List(t.Context(), memberScope)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != memberDevice.ID {
		t.Fatalf("a member saw %d devices, want only their own", len(listed))
	}

	// Another user's device is reported as missing rather than forbidden:
	// "you may not see this" would confirm it exists.
	if _, err := f.store.ByID(t.Context(), adminDevice.ID, memberScope); !errors.Is(err, devices.ErrDeviceNotFound) {
		t.Fatalf("error = %v, want ErrDeviceNotFound for another user's device", err)
	}
}

func TestAMemberCannotRevokeAnotherUsersDevice(t *testing.T) {
	f := newFixture(t)
	member := f.newUser(t, "member@example.com", auth.RoleMember)

	adminDevice := f.register(t, "admin-laptop")

	err := f.store.Revoke(t.Context(), adminDevice.ID,
		devices.ScopeFor(member.ID, false), time.Now().UTC())
	if !errors.Is(err, devices.ErrDeviceNotFound) {
		t.Fatalf("error = %v, want the revocation refused", err)
	}

	// And it really is still active.
	reloaded, err := f.store.ByID(t.Context(), adminDevice.ID, devices.ScopeFor(f.admin.ID, true))
	if err != nil {
		t.Fatalf("ByID failed: %v", err)
	}
	if reloaded.Revoked() {
		t.Fatal("a member revoked another user's device")
	}
}

func TestAnAdminSeesEveryDevice(t *testing.T) {
	f := newFixture(t)
	member := f.newUser(t, "member@example.com", auth.RoleMember)
	now := time.Now().UTC()

	f.register(t, "admin-laptop")
	if _, err := f.store.Register(t.Context(), member.ID,
		devices.NewDevice{Name: "member-laptop", PublicKey: publicKey(t)}, now); err != nil {
		t.Fatalf("registering the member device failed: %v", err)
	}

	listed, err := f.store.List(t.Context(), devices.ScopeFor(f.admin.ID, true))
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("an admin saw %d devices, want 2", len(listed))
	}
}

func TestEnrolWithASetupKey(t *testing.T) {
	f := newFixture(t)
	issued := f.createKey(t, devices.NewSetupKey{Description: "build agents"})

	device, _, err := f.store.Enroll(t.Context(), issued.Token, devices.NewDevice{
		Name: "agent-1", PublicKey: publicKey(t), OS: "linux",
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("Enroll failed: %v", err)
	}

	if device.EnrolledWith != issued.Key.ID {
		t.Errorf("EnrolledWith = %q, want the setup key %q", device.EnrolledWith, issued.Key.ID)
	}
	if device.UserID != f.admin.ID {
		t.Errorf("UserID = %q, want the key's creator", device.UserID)
	}
	if !device.IPv4.IsValid() {
		t.Error("an enrolled device got no address")
	}

	// The redemption was recorded.
	reloaded, err := f.store.SetupKeyByID(t.Context(), issued.Key.ID)
	if err != nil {
		t.Fatalf("SetupKeyByID failed: %v", err)
	}
	if reloaded.Uses != 1 {
		t.Fatalf("Uses = %d, want 1", reloaded.Uses)
	}
}

func TestASingleUseKeyWorksExactlyOnce(t *testing.T) {
	f := newFixture(t)
	issued := f.createKey(t, devices.NewSetupKey{MaxUses: 1})
	now := time.Now().UTC()

	if _, _, err := f.store.Enroll(t.Context(), issued.Token,
		devices.NewDevice{Name: "first", PublicKey: publicKey(t)}, now); err != nil {
		t.Fatalf("the first enrolment failed: %v", err)
	}

	_, _, err := f.store.Enroll(t.Context(), issued.Token,
		devices.NewDevice{Name: "second", PublicKey: publicKey(t)}, now)
	if !errors.Is(err, devices.ErrKeyExhausted) {
		t.Fatalf("error = %v, want ErrKeyExhausted", err)
	}
}

func TestAnUnlimitedKeyKeepsWorking(t *testing.T) {
	f := newFixture(t)
	issued := f.createKey(t, devices.NewSetupKey{MaxUses: 0})
	now := time.Now().UTC()

	for i := range 5 {
		if _, _, err := f.store.Enroll(t.Context(), issued.Token,
			devices.NewDevice{Name: "agent-" + strconv.Itoa(i), PublicKey: publicKey(t)}, now); err != nil {
			t.Fatalf("enrolment %d failed: %v", i, err)
		}
	}
}

func TestConcurrentRedemptionOfASingleUseKey(t *testing.T) {
	// The check lives in the UPDATE's WHERE clause, not in a read-then-write
	// comparison, so two enrolments racing for the last use cannot both win.
	//
	// SQLite serialises on one connection and cannot actually contend; under
	// PostgreSQL this is a real race. That is why the suite runs on both.
	f := newFixture(t)
	issued := f.createKey(t, devices.NewSetupKey{MaxUses: 1})

	const attempts = 8
	var wg sync.WaitGroup
	errs := make([]error, attempts)

	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = f.store.Enroll(t.Context(), issued.Token, devices.NewDevice{
				Name: "agent-" + strconv.Itoa(i), PublicKey: publicKey(t),
			}, time.Now().UTC())
		}()
	}
	wg.Wait()

	succeeded := 0
	for i, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, devices.ErrKeyExhausted):
			// Expected for the losers.
		default:
			t.Errorf("attempt %d gave an unexpected error: %v", i, err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("%d of %d concurrent redemptions succeeded, want exactly 1", succeeded, attempts)
	}

	count, err := f.store.Count(t.Context(), devices.ScopeFor(f.admin.ID, true))
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("%d devices were created, want 1", count)
	}
}

func TestEnrolIsRefusedForAnUnusableKey(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UTC()

	revoked := f.createKey(t, devices.NewSetupKey{})
	if err := f.store.RevokeSetupKey(t.Context(), revoked.Key.ID, now); err != nil {
		t.Fatalf("RevokeSetupKey failed: %v", err)
	}
	expired := f.createKey(t, devices.NewSetupKey{ExpiresIn: time.Hour})

	tests := []struct {
		name  string
		token string
		when  time.Time
		want  error
	}{
		{"revoked", revoked.Token, now, devices.ErrKeyRevoked},
		{"expired", expired.Token, now.Add(2 * time.Hour), devices.ErrKeyExpired},
		{"unknown", "not-a-real-key", now, devices.ErrKeyNotFound},
		{"empty", "", now, devices.ErrKeyNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := f.store.Enroll(t.Context(), tt.token,
				devices.NewDevice{Name: "agent", PublicKey: publicKey(t)}, tt.when)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestAFailedEnrolmentDoesNotSpendTheKey(t *testing.T) {
	// The redemption and the device creation are one transaction. If a
	// rejected device still burned the use, one typo would waste a single-use
	// key and the operator would have to issue another.
	f := newFixture(t)
	issued := f.createKey(t, devices.NewSetupKey{MaxUses: 1})
	now := time.Now().UTC()

	// A duplicate public key, so the device insert fails after the key is
	// consumed.
	key := publicKey(t)
	if _, err := f.store.Register(t.Context(), f.admin.ID,
		devices.NewDevice{Name: "existing", PublicKey: key}, now); err != nil {
		t.Fatalf("seeding failed: %v", err)
	}

	if _, _, err := f.store.Enroll(t.Context(), issued.Token,
		devices.NewDevice{Name: "doomed", PublicKey: key}, now); err == nil {
		t.Fatal("enrolment with a duplicate key succeeded")
	}

	reloaded, err := f.store.SetupKeyByID(t.Context(), issued.Key.ID)
	if err != nil {
		t.Fatalf("SetupKeyByID failed: %v", err)
	}
	if reloaded.Uses != 0 {
		t.Fatalf("Uses = %d after a failed enrolment, want 0", reloaded.Uses)
	}

	// And the key still works for a good device.
	if _, _, err := f.store.Enroll(t.Context(), issued.Token,
		devices.NewDevice{Name: "good", PublicKey: publicKey(t)}, now); err != nil {
		t.Fatalf("the key stopped working after a failed enrolment: %v", err)
	}
}

func TestPoolExhaustionIsReported(t *testing.T) {
	// /30 leaves exactly two usable addresses.
	f := newFixtureWithPools(t, "100.100.0.0/30", "")

	f.register(t, "one")
	f.register(t, "two")

	_, err := f.store.Register(t.Context(), f.admin.ID,
		devices.NewDevice{Name: "three", PublicKey: publicKey(t)}, time.Now().UTC())
	if !errors.Is(err, network.ErrPoolExhausted) {
		t.Fatalf("error = %v, want ErrPoolExhausted", err)
	}
}

func TestRecordSeen(t *testing.T) {
	f := newFixture(t)
	scope := devices.ScopeFor(f.admin.ID, true)
	device := f.register(t, "laptop")

	when := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	if err := f.store.RecordSeen(t.Context(), device.ID, when); err != nil {
		t.Fatalf("RecordSeen failed: %v", err)
	}

	reloaded, err := f.store.ByID(t.Context(), device.ID, scope)
	if err != nil {
		t.Fatalf("ByID failed: %v", err)
	}
	if reloaded.LastSeenAt == nil || !reloaded.LastSeenAt.UTC().Equal(when) {
		t.Fatalf("LastSeenAt = %v, want %v", reloaded.LastSeenAt, when)
	}
}

func TestARevokedDeviceDoesNotCheckIn(t *testing.T) {
	f := newFixture(t)
	scope := devices.ScopeFor(f.admin.ID, true)
	device := f.register(t, "laptop")
	now := time.Now().UTC()

	if err := f.store.Revoke(t.Context(), device.ID, scope, now); err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}
	if err := f.store.RecordSeen(t.Context(), device.ID, now.Add(time.Hour)); err != nil {
		t.Fatalf("RecordSeen failed: %v", err)
	}

	reloaded, err := f.store.ByID(t.Context(), device.ID, scope)
	if err != nil {
		t.Fatalf("ByID failed: %v", err)
	}
	if reloaded.LastSeenAt != nil {
		t.Fatal("a revoked device recorded a check-in")
	}
}

func TestDeletingAUserDeletesTheirDevices(t *testing.T) {
	f := newFixture(t)
	member := f.newUser(t, "member@example.com", auth.RoleMember)

	device, err := f.store.Register(t.Context(), member.ID,
		devices.NewDevice{Name: "laptop", PublicKey: publicKey(t)}, time.Now().UTC())
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if _, err := f.db.ExecContext(t.Context(),
		f.db.Rebind(`DELETE FROM users WHERE id = ?`), member.ID); err != nil {
		t.Fatalf("deleting the user failed: %v", err)
	}

	_, err = f.store.ByID(t.Context(), device.ID, devices.ScopeFor(f.admin.ID, true))
	if !errors.Is(err, devices.ErrDeviceNotFound) {
		t.Fatalf("error = %v, want the device to have been cascaded away", err)
	}
}

func TestDeletingASetupKeyKeepsItsDevices(t *testing.T) {
	// ON DELETE SET NULL, not CASCADE. Losing the audit trail would be bad;
	// losing the machines would be worse.
	f := newFixture(t)
	issued := f.createKey(t, devices.NewSetupKey{})

	device, _, err := f.store.Enroll(t.Context(), issued.Token,
		devices.NewDevice{Name: "agent", PublicKey: publicKey(t)}, time.Now().UTC())
	if err != nil {
		t.Fatalf("Enroll failed: %v", err)
	}

	if _, err := f.db.ExecContext(t.Context(),
		f.db.Rebind(`DELETE FROM setup_keys WHERE id = ?`), issued.Key.ID); err != nil {
		t.Fatalf("deleting the setup key failed: %v", err)
	}

	reloaded, err := f.store.ByID(t.Context(), device.ID, devices.ScopeFor(f.admin.ID, true))
	if err != nil {
		t.Fatalf("the device did not survive its setup key: %v", err)
	}
	if reloaded.EnrolledWith != "" {
		t.Errorf("EnrolledWith = %q, want it cleared", reloaded.EnrolledWith)
	}
}

func TestTheDeviceRecordHoldsNoPrivateKeyMaterial(t *testing.T) {
	// The whole security argument for the control plane rests on this: a
	// stolen database must yield nothing that can decrypt traffic.
	f := newFixture(t)
	device := f.register(t, "laptop")

	rows, err := f.db.QueryContext(t.Context(), `SELECT * FROM devices`)
	if err != nil {
		t.Fatalf("reading devices failed: %v", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("reading columns failed: %v", err)
	}
	for _, column := range columns {
		lowered := strings.ToLower(column)
		if strings.Contains(lowered, "private") || strings.Contains(lowered, "secret") {
			t.Fatalf("the devices table has a %q column; it must hold no private key material", column)
		}
	}
	if device.PublicKey == "" {
		t.Fatal("the device has no public key")
	}
}
