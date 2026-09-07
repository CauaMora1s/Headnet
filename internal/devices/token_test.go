package devices_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CauaMora1s/Headnet/internal/auth"
	"github.com/CauaMora1s/Headnet/internal/devices"
)

func (f *fixture) enrolToken(t *testing.T) (*devices.Device, string) {
	t.Helper()
	key := f.createKey(t, devices.NewSetupKey{MaxUses: 1})
	d, token, err := f.store.Enroll(t.Context(), key.Token,
		devices.NewDevice{Name: "agent", PublicKey: publicKey(t)}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return d, token
}

func TestDeviceCredentialIsHashedScopedAndRevocable(t *testing.T) {
	// A database read must not yield a replayable credential; revocation must
	// invalidate both authentication and writes without affecting another device.
	f := newFixture(t)
	d, token := f.enrolToken(t)
	other, otherToken := f.enrolToken(t)
	var stored string
	if err := f.db.QueryRowContext(t.Context(), f.db.Rebind(
		`SELECT token_hash FROM device_tokens WHERE device_id = ?`), d.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != auth.HashToken(token) || stored == token {
		t.Fatal("credential was not stored as a hash")
	}
	for _, invalid := range []string{"", stored, token + "x", "hnd_" + strings.Repeat("A", 43)} {
		if _, err := f.store.Authenticate(t.Context(), invalid); !errors.Is(err, devices.ErrDeviceTokenInvalid) {
			t.Errorf("invalid credential accepted: %v", err)
		}
	}
	got, err := f.store.Authenticate(t.Context(), token)
	if err != nil || got.ID != d.ID {
		t.Fatalf("credential did not identify its device: %v", err)
	}
	// PostgreSQL persists microseconds; use the common precision of both stores.
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := f.store.Heartbeat(t.Context(), token, now); err != nil {
		t.Fatal(err)
	}
	got, err = f.store.ByID(t.Context(), d.ID, devices.Scope{All: true})
	if err != nil || got.LastSeenAt == nil || !got.LastSeenAt.Equal(now) {
		t.Fatalf("heartbeat was not recorded: %v", err)
	}
	if err := f.store.Revoke(t.Context(), d.ID, devices.Scope{All: true}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Authenticate(t.Context(), token); !errors.Is(err, devices.ErrDeviceTokenInvalid) {
		t.Fatalf("revoked credential authenticated: %v", err)
	}
	if err := f.store.Heartbeat(t.Context(), token, now.Add(time.Hour)); !errors.Is(err, devices.ErrDeviceTokenInvalid) {
		t.Fatalf("revoked credential checked in: %v", err)
	}
	var n int
	if err := f.db.QueryRowContext(t.Context(), f.db.Rebind(
		`SELECT COUNT(*) FROM device_tokens WHERE device_id = ?`), d.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("revocation retained a credential row: count %d, error %v", n, err)
	}
	got, err = f.store.Authenticate(t.Context(), otherToken)
	if err != nil || got.ID != other.ID {
		t.Fatalf("revocation affected a different device: %v", err)
	}
	// Re-enrolment uses a fresh public key and token; it cannot resurrect the
	// old token even when the allocator reuses the revoked device's address.
	replacement, replacementToken := f.enrolToken(t)
	if replacementToken == token || replacement.IPv4 != d.IPv4 {
		t.Fatal("replacement did not get a fresh credential with the released address")
	}
	if _, err := f.store.Authenticate(t.Context(), token); !errors.Is(err, devices.ErrDeviceTokenInvalid) {
		t.Fatalf("re-enrolment resurrected the old credential: %v", err)
	}
}

func TestDeviceCredentialInsertFailureRollsBackEnrolment(t *testing.T) {
	// Failure in the final credential write must roll back address allocation,
	// device insertion and single-use redemption on both SQL backends.
	f := newFixture(t)
	key := f.createKey(t, devices.NewSetupKey{MaxUses: 1})
	if _, err := f.db.ExecContext(t.Context(), `DROP TABLE device_tokens`); err != nil {
		t.Fatal(err)
	}
	d, token, err := f.store.Enroll(t.Context(), key.Token,
		devices.NewDevice{Name: "agent", PublicKey: publicKey(t)}, time.Now().UTC())
	if err == nil || d != nil || token != "" {
		t.Fatal("failed credential persistence returned a device or credential")
	}
	for _, table := range []string{"devices", "ip_allocations"} {
		var n int
		if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("failed enrolment left rows in %s: count %d, error %v", table, n, err)
		}
	}
	got, err := f.store.SetupKeyByToken(t.Context(), nil, key.Token)
	if err != nil || got.Uses != 0 {
		t.Fatalf("failed enrolment spent the setup key: %v", err)
	}
}

func TestConcurrentDeviceRevocationAndHeartbeat(t *testing.T) {
	// PostgreSQL executes the race with real concurrent connections. A
	// heartbeat may complete first; none may change last_seen after revocation.
	f := newFixture(t)
	d, token := f.enrolToken(t)
	const attempts = 8
	errs := make([]error, attempts)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = f.store.Heartbeat(t.Context(), token, time.Now().UTC())
		}()
	}
	close(start)
	if err := f.store.Revoke(t.Context(), d.ID, devices.Scope{All: true}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil && !errors.Is(err, devices.ErrDeviceTokenInvalid) {
			t.Errorf("unexpected race error: %v", err)
		}
	}
	before, err := f.store.ByID(t.Context(), d.ID, devices.Scope{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Heartbeat(t.Context(), token, time.Now().Add(time.Hour)); !errors.Is(err, devices.ErrDeviceTokenInvalid) {
		t.Fatalf("post-revocation heartbeat succeeded: %v", err)
	}
	after, err := f.store.ByID(t.Context(), d.ID, devices.Scope{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if (before.LastSeenAt == nil) != (after.LastSeenAt == nil) ||
		(before.LastSeenAt != nil && !before.LastSeenAt.Equal(*after.LastSeenAt)) {
		t.Fatal("last_seen changed after revocation")
	}
}

func TestCredentialDeletionFailureRollsBackRevocation(t *testing.T) {
	// A failure removing the credential must not leave a partially revoked
	// device or release its address while the revocation transaction failed.
	f := newFixture(t)
	d, _ := f.enrolToken(t)
	if _, err := f.db.ExecContext(t.Context(), `DROP TABLE device_tokens`); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Revoke(t.Context(), d.ID, devices.Scope{All: true}, time.Now().UTC()); err == nil {
		t.Fatal("revocation succeeded despite credential deletion failure")
	}
	got, err := f.store.ByID(t.Context(), d.ID, devices.Scope{All: true})
	if err != nil || got.Revoked() || got.IPv4 != d.IPv4 {
		t.Fatalf("failed revocation changed the device: %v", err)
	}
	var n int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM ip_allocations`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("failed revocation released an address: count %d, error %v", n, err)
	}
}

func TestDeletingDeviceOwnerCascadesCredential(t *testing.T) {
	// Removing an account must not leave its former device credential usable.
	f := newFixture(t)
	_, token := f.enrolToken(t)
	if _, err := f.db.ExecContext(t.Context(), f.db.Rebind(`DELETE FROM users WHERE id = ?`), f.admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Authenticate(t.Context(), token); !errors.Is(err, devices.ErrDeviceTokenInvalid) {
		t.Fatalf("credential survived account deletion: %v", err)
	}
	var n int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM device_tokens`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("account deletion left credentials: count %d, error %v", n, err)
	}
}
