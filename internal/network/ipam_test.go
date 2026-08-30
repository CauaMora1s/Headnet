package network_test

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/CauaMora1s/Headnet/internal/config"
	"github.com/CauaMora1s/Headnet/internal/network"
	"github.com/CauaMora1s/Headnet/internal/storage"
)

// newAllocator returns an allocator over a migrated in-memory database, with
// the pools narrowed so exhaustion can be reached in a test.
func newAllocator(t *testing.T, v4CIDR, v6CIDR string) (*network.Allocator, *storage.DB) {
	t.Helper()

	db, err := storage.Open(t.Context(), storage.Options{
		Driver: storage.DriverSQLite,
		Path:   storage.MemoryPath,
	})
	if err != nil {
		t.Fatalf("opening the database failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := storage.Migrate(t.Context(), db, nil); err != nil {
		t.Fatalf("migrating failed: %v", err)
	}

	cfg := config.Default().Network
	cfg.IPv4CIDR = v4CIDR
	cfg.IPv6CIDR = v6CIDR

	allocator, err := network.NewAllocator(db, cfg)
	if err != nil {
		t.Fatalf("NewAllocator failed: %v", err)
	}
	return allocator, db
}

// allocate runs one allocation in its own committed transaction.
func allocate(t *testing.T, a *network.Allocator, db *storage.DB, owner string) network.Assignment {
	t.Helper()
	var got network.Assignment
	err := db.InTx(t.Context(), func(tx *sql.Tx) error {
		var err error
		got, err = a.Allocate(t.Context(), tx, owner, time.Now().UTC())
		return err
	})
	if err != nil {
		t.Fatalf("allocating for %s failed: %v", owner, err)
	}
	return got
}

func TestAllocatesTheLowestFreeAddress(t *testing.T) {
	a, db := newAllocator(t, "100.100.0.0/16", "")

	for i, want := range []string{"100.100.0.1", "100.100.0.2", "100.100.0.3"} {
		got := allocate(t, a, db, "dev_"+strconv.Itoa(i))
		if got.IPv4.String() != want {
			t.Fatalf("allocation %d = %s, want %s", i, got.IPv4, want)
		}
	}
}

func TestNetworkAddressIsNeverHandedOut(t *testing.T) {
	// 100.100.0.0 is the network address; issuing it would give a device an
	// address that names the whole subnet.
	a, db := newAllocator(t, "100.100.0.0/16", "")

	got := allocate(t, a, db, "dev_1")
	if got.IPv4 == netip.MustParseAddr("100.100.0.0") {
		t.Fatal("the network address was allocated")
	}
}

func TestBroadcastAddressIsNeverHandedOut(t *testing.T) {
	// A /30 has exactly two usable hosts: .1 and .2. The .0 network and .3
	// broadcast addresses must both be skipped.
	a, db := newAllocator(t, "100.100.0.0/30", "")

	first := allocate(t, a, db, "dev_1")
	second := allocate(t, a, db, "dev_2")

	if first.IPv4.String() != "100.100.0.1" || second.IPv4.String() != "100.100.0.2" {
		t.Fatalf("allocated %s and %s, want 100.100.0.1 and 100.100.0.2", first.IPv4, second.IPv4)
	}

	// The third must fail rather than hand out the broadcast address.
	err := db.InTx(t.Context(), func(tx *sql.Tx) error {
		_, err := a.Allocate(t.Context(), tx, "dev_3", time.Now())
		return err
	})
	if !errors.Is(err, network.ErrPoolExhausted) {
		t.Fatalf("error = %v, want ErrPoolExhausted rather than the broadcast address", err)
	}
}

func TestNoAddressIsIssuedTwice(t *testing.T) {
	a, db := newAllocator(t, "100.100.0.0/24", "")

	seen := make(map[netip.Addr]string)
	for i := range 50 {
		owner := "dev_" + strconv.Itoa(i)
		got := allocate(t, a, db, owner)
		if previous, dup := seen[got.IPv4]; dup {
			t.Fatalf("%s was issued to both %s and %s", got.IPv4, previous, owner)
		}
		seen[got.IPv4] = owner
	}
}

func TestConcurrentAllocationNeverDuplicates(t *testing.T) {
	// The scan for a free address is only a way to pick a candidate; the
	// primary key on ip_allocations.address is what actually guarantees
	// uniqueness.
	//
	// Note what this does and does not prove. SQLite holds the pool to a
	// single connection, so these transactions serialise and the insert never
	// actually loses a race — the retry path is not exercised here. Genuine
	// contention needs PostgreSQL, where these same tests run in CI with a
	// real connection pool. This case guards the invariant; that job proves
	// the retry works.
	a, db := newAllocator(t, "100.100.0.0/24", "")

	const workers = 25
	var wg sync.WaitGroup
	results := make([]network.Assignment, workers)
	errs := make([]error, workers)

	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = db.InTx(context.Background(), func(tx *sql.Tx) error {
				var err error
				results[i], err = a.Allocate(context.Background(), tx, "dev_"+strconv.Itoa(i), time.Now().UTC())
				return err
			})
		}()
	}
	wg.Wait()

	seen := make(map[netip.Addr]int)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d failed: %v", i, err)
		}
		addr := results[i].IPv4
		if previous, dup := seen[addr]; dup {
			t.Fatalf("%s was issued to both worker %d and worker %d", addr, previous, i)
		}
		seen[addr] = i
	}
	if len(seen) != workers {
		t.Fatalf("got %d distinct addresses, want %d", len(seen), workers)
	}
}

func TestReleaseFreesTheAddressForReuse(t *testing.T) {
	a, db := newAllocator(t, "100.100.0.0/16", "")

	first := allocate(t, a, db, "dev_1")

	var released int
	err := db.InTx(t.Context(), func(tx *sql.Tx) error {
		var err error
		released, err = a.Release(t.Context(), tx, "dev_1")
		return err
	})
	if err != nil {
		t.Fatalf("Release failed: %v", err)
	}
	if released != 1 {
		t.Fatalf("Release freed %d addresses, want 1", released)
	}

	// The freed address is the lowest free one again, so it comes back.
	reused := allocate(t, a, db, "dev_2")
	if reused.IPv4 != first.IPv4 {
		t.Fatalf("reallocated %s, want the released %s", reused.IPv4, first.IPv4)
	}
}

func TestReleaseIsSafeForAnOwnerWithNoAddresses(t *testing.T) {
	a, db := newAllocator(t, "100.100.0.0/16", "")

	var released int
	err := db.InTx(t.Context(), func(tx *sql.Tx) error {
		var err error
		released, err = a.Release(t.Context(), tx, "dev_nonexistent")
		return err
	})
	if err != nil {
		t.Fatalf("Release failed: %v", err)
	}
	if released != 0 {
		t.Fatalf("Release freed %d addresses, want 0", released)
	}
}

func TestReleaseFreesEveryFamily(t *testing.T) {
	a, db := newAllocator(t, "100.100.0.0/16", "fd7a:115c:a1e0::/48")

	allocate(t, a, db, "dev_1")

	var released int
	err := db.InTx(t.Context(), func(tx *sql.Tx) error {
		var err error
		released, err = a.Release(t.Context(), tx, "dev_1")
		return err
	})
	if err != nil {
		t.Fatalf("Release failed: %v", err)
	}
	if released != 2 {
		t.Fatalf("Release freed %d addresses, want both the IPv4 and the IPv6", released)
	}
}

func TestPoolExhaustion(t *testing.T) {
	a, db := newAllocator(t, "100.100.0.0/30", "")

	allocate(t, a, db, "dev_1")
	allocate(t, a, db, "dev_2")

	err := db.InTx(t.Context(), func(tx *sql.Tx) error {
		_, err := a.Allocate(t.Context(), tx, "dev_3", time.Now())
		return err
	})
	if !errors.Is(err, network.ErrPoolExhausted) {
		t.Fatalf("error = %v, want it to wrap ErrPoolExhausted", err)
	}
}

func TestIPv6IsAssignedWhenEnabled(t *testing.T) {
	a, db := newAllocator(t, "100.100.0.0/16", "fd7a:115c:a1e0::/48")

	if !a.IPv6Enabled() {
		t.Fatal("IPv6Enabled() = false for a configured IPv6 pool")
	}

	got := allocate(t, a, db, "dev_1")
	if !got.HasIPv6() {
		t.Fatal("no IPv6 address was assigned")
	}
	if got.IPv6.String() != "fd7a:115c:a1e0::1" {
		t.Fatalf("IPv6 = %s, want fd7a:115c:a1e0::1", got.IPv6)
	}
	if len(got.Addrs()) != 2 {
		t.Fatalf("Addrs() = %v, want both families", got.Addrs())
	}
}

func TestIPv4OnlyDeploymentAssignsNoIPv6(t *testing.T) {
	a, db := newAllocator(t, "100.100.0.0/16", "")

	if a.IPv6Enabled() {
		t.Fatal("IPv6Enabled() = true for an IPv4-only deployment")
	}

	got := allocate(t, a, db, "dev_1")
	if got.HasIPv6() {
		t.Fatalf("an IPv6 address (%s) was assigned on an IPv4-only deployment", got.IPv6)
	}
	if len(got.Addrs()) != 1 {
		t.Fatalf("Addrs() = %v, want only the IPv4 address", got.Addrs())
	}
}

func TestRolledBackTransactionLeavesNoAllocation(t *testing.T) {
	// Allocation runs inside the caller's transaction precisely so that a
	// device and its address are created or discarded together. If a failed
	// enrolment leaked an address, the pool would bleed on every retry.
	a, db := newAllocator(t, "100.100.0.0/16", "")

	sentinel := errors.New("the enrolment failed after allocating")
	err := db.InTx(t.Context(), func(tx *sql.Tx) error {
		if _, err := a.Allocate(t.Context(), tx, "dev_doomed", time.Now().UTC()); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("InTx error = %v, want the callback error", err)
	}

	got, err := a.AddressesOf(t.Context(), "dev_doomed")
	if err != nil {
		t.Fatalf("AddressesOf failed: %v", err)
	}
	if len(got.Addrs()) != 0 {
		t.Fatalf("the rolled-back allocation survived: %v", got.Addrs())
	}

	// And the address is still available to the next caller.
	next := allocate(t, a, db, "dev_1")
	if next.IPv4.String() != "100.100.0.1" {
		t.Fatalf("next allocation = %s, want the address to have been freed", next.IPv4)
	}
}

func TestAllocatingTwiceForOneOwnerIsRefused(t *testing.T) {
	// A second allocation would silently strand the first, leaking an address
	// and leaving the owner with an address nothing knows about.
	a, db := newAllocator(t, "100.100.0.0/16", "")

	allocate(t, a, db, "dev_1")

	err := db.InTx(t.Context(), func(tx *sql.Tx) error {
		_, err := a.Allocate(t.Context(), tx, "dev_1", time.Now())
		return err
	})
	if !errors.Is(err, network.ErrAlreadyAllocated) {
		t.Fatalf("error = %v, want it to wrap ErrAlreadyAllocated", err)
	}
}

func TestAddressesOf(t *testing.T) {
	a, db := newAllocator(t, "100.100.0.0/16", "fd7a:115c:a1e0::/48")

	allocated := allocate(t, a, db, "dev_1")

	got, err := a.AddressesOf(t.Context(), "dev_1")
	if err != nil {
		t.Fatalf("AddressesOf failed: %v", err)
	}
	if got.IPv4 != allocated.IPv4 || got.IPv6 != allocated.IPv6 {
		t.Fatalf("AddressesOf = %v, want %v", got.Addrs(), allocated.Addrs())
	}

	empty, err := a.AddressesOf(t.Context(), "dev_unknown")
	if err != nil {
		t.Fatalf("AddressesOf failed: %v", err)
	}
	if len(empty.Addrs()) != 0 {
		t.Fatalf("AddressesOf for an unknown owner = %v, want nothing", empty.Addrs())
	}
}

func TestAllocateRequiresATransactionAndAnOwner(t *testing.T) {
	a, db := newAllocator(t, "100.100.0.0/16", "")

	if _, err := a.Allocate(t.Context(), nil, "dev_1", time.Now()); err == nil {
		t.Error("Allocate accepted a nil transaction")
	}
	err := db.InTx(t.Context(), func(tx *sql.Tx) error {
		_, err := a.Allocate(t.Context(), tx, "", time.Now())
		return err
	})
	if err == nil {
		t.Error("Allocate accepted an empty owner ID")
	}
}

func TestNewAllocatorRejectsAnInvalidPool(t *testing.T) {
	db, err := storage.Open(t.Context(), storage.Options{
		Driver: storage.DriverSQLite, Path: storage.MemoryPath,
	})
	if err != nil {
		t.Fatalf("opening the database failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// An allocator over a loopback pool would hand out addresses that shadow a
	// device's own loopback traffic, so it must be refused even though the
	// caller skipped Config.Validate.
	cfg := config.Default().Network
	cfg.IPv4CIDR = "127.0.0.0/8"
	if _, err := network.NewAllocator(db, cfg); err == nil {
		t.Error("NewAllocator accepted a loopback pool")
	}

	if _, err := network.NewAllocator(nil, config.Default().Network); err == nil {
		t.Error("NewAllocator accepted a nil database handle")
	}
}

func TestStaleAllocationsOutsideThePoolDoNotBlockAllocation(t *testing.T) {
	// An operator who re-addresses the network leaves rows behind from the old
	// pool. They must be ignored rather than counted as taken.
	a, db := newAllocator(t, "100.100.0.0/16", "")

	_, err := db.ExecContext(t.Context(),
		`INSERT INTO ip_allocations (address, family, owner_id, allocated_at) VALUES ('10.9.9.9', 4, 'dev_old', ?)`,
		time.Now().UTC())
	if err != nil {
		t.Fatalf("seeding a stale allocation failed: %v", err)
	}

	got := allocate(t, a, db, "dev_1")
	if got.IPv4.String() != "100.100.0.1" {
		t.Fatalf("allocated %s, want the stale row to be ignored", got.IPv4)
	}
}
