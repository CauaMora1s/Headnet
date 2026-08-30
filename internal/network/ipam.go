// Package network allocates addresses from the overlay pools.
//
// Every device on a Headnet network needs a stable private address. This
// package hands them out from the pools configured in internal/config, whose
// validation rules (reserved ranges, host bits, prefix sizes, family
// mismatches) are enforced before a server ever starts.
//
// The design decision that matters here is where correctness comes from.
// Choosing a free address by scanning is only a way to pick a candidate; two
// servers, or two concurrent enrolments, can pick the same one. The guarantee
// that an address is never issued twice is the primary key on
// ip_allocations.address, enforced by the database. The scan is an
// optimisation; the constraint is the contract.
//
// Allocation always runs inside a transaction supplied by the caller, so that
// a device and its address are created or discarded together. That is also why
// ip_allocations carries no foreign key to devices: the transaction, not a
// constraint, is what keeps the two in step.
package network

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"time"

	"github.com/CauaMora1s/Headnet/internal/config"
	"github.com/CauaMora1s/Headnet/internal/storage"
)

// Sentinel errors callers are expected to branch on.
var (
	// ErrPoolExhausted means every usable address in a pool is taken. The
	// operator has to widen the pool, which is a re-addressing exercise, so
	// this is worth surfacing long before it happens.
	ErrPoolExhausted = errors.New("the address pool is exhausted")

	// ErrAlreadyAllocated means the owner already holds an address in that
	// family. Allocating a second one would silently strand the first, so it
	// is refused; callers wanting the existing value should use AddressesOf.
	ErrAlreadyAllocated = errors.New("the owner already holds an address")
)

const (
	// maxAllocationAttempts bounds the retry loop that runs when another
	// transaction takes the candidate address first. The bound exists to turn
	// a pathological case into a clear error rather than a hang.
	maxAllocationAttempts = 32

	// scatterWindow is how many free addresses a retry chooses between.
	//
	// Retrying in strict lowest-first order does not work under real
	// concurrency: every contending allocator reads the same table, picks the
	// same address, and then walks upwards in lockstep, so the last of N
	// simultaneous enrolments needs N attempts. Choosing at random from a
	// window of free addresses breaks that lockstep, and convergence stops
	// depending on how many enrolments happen to overlap.
	scatterWindow = 256
)

// Assignment is the set of addresses held by one owner.
type Assignment struct {
	// IPv4 is always valid for a successful allocation.
	IPv4 netip.Addr
	// IPv6 is invalid when the deployment is IPv4-only.
	IPv6 netip.Addr
}

// HasIPv6 reports whether an IPv6 address was assigned.
func (a Assignment) HasIPv6() bool { return a.IPv6.IsValid() }

// Addrs returns the assigned addresses, IPv4 first, omitting any that are
// unset.
func (a Assignment) Addrs() []netip.Addr {
	out := make([]netip.Addr, 0, 2)
	if a.IPv4.IsValid() {
		out = append(out, a.IPv4)
	}
	if a.IPv6.IsValid() {
		out = append(out, a.IPv6)
	}
	return out
}

// Allocator hands out addresses from the configured pools.
type Allocator struct {
	db *storage.DB

	v4 netip.Prefix
	v6 netip.Prefix // zero value when IPv6 is disabled

	// Precomputed broadcast addresses, which must never be handed out.
	v4Broadcast netip.Addr
}

// NewAllocator builds an allocator for the configured pools.
//
// The configuration must already have passed config.Validate; this repeats
// the parse rather than trusting the caller, because an allocator built from
// an invalid pool would hand out addresses that shadow a device's own
// loopback or link-local traffic.
func NewAllocator(db *storage.DB, cfg config.NetworkConfig) (*Allocator, error) {
	if db == nil {
		return nil, errors.New("network: a database handle is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("network: %w", err)
	}

	a := &Allocator{db: db, v4: cfg.IPv4Pool()}
	a.v4Broadcast = lastAddr(a.v4)

	if v6, enabled := cfg.IPv6Pool(); enabled {
		a.v6 = v6
	}
	return a, nil
}

// IPv6Enabled reports whether the deployment hands out IPv6 addresses.
func (a *Allocator) IPv6Enabled() bool { return a.v6.IsValid() }

// Allocate assigns one address per enabled family to ownerID.
//
// It must run inside the same transaction as whatever creates the owner, so
// that a failure leaves neither behind.
func (a *Allocator) Allocate(ctx context.Context, tx *sql.Tx, ownerID string, now time.Time) (Assignment, error) {
	if tx == nil {
		return Assignment{}, errors.New("network: allocation requires a transaction")
	}
	if ownerID == "" {
		return Assignment{}, errors.New("network: an owner ID is required")
	}

	existing, err := a.addressesOf(ctx, tx, ownerID)
	if err != nil {
		return Assignment{}, err
	}
	if len(existing.Addrs()) > 0 {
		return Assignment{}, fmt.Errorf("%w: %s", ErrAlreadyAllocated, ownerID)
	}

	var assignment Assignment
	assignment.IPv4, err = a.allocateFrom(ctx, tx, a.v4, ownerID, now)
	if err != nil {
		return Assignment{}, fmt.Errorf("allocating an IPv4 address: %w", err)
	}

	if a.IPv6Enabled() {
		assignment.IPv6, err = a.allocateFrom(ctx, tx, a.v6, ownerID, now)
		if err != nil {
			// The IPv4 allocation above is undone by the caller rolling the
			// transaction back, which is precisely why one is required.
			return Assignment{}, fmt.Errorf("allocating an IPv6 address: %w", err)
		}
	}
	return assignment, nil
}

// allocateFrom claims the lowest free address in one pool.
func (a *Allocator) allocateFrom(
	ctx context.Context, tx *sql.Tx, pool netip.Prefix, ownerID string, now time.Time,
) (netip.Addr, error) {
	taken, err := a.takenIn(ctx, tx, pool)
	if err != nil {
		return netip.Addr{}, err
	}

	insert := a.db.Rebind(`
		INSERT INTO ip_allocations (address, family, owner_id, allocated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (address) DO NOTHING`)

	for attempt := range maxAllocationAttempts {
		// The first attempt takes the lowest free address, so a quiet system
		// numbers its devices tidily and predictably. Only a collision falls
		// back to scattering.
		candidate, err := a.pickCandidate(pool, taken, attempt > 0)
		if err != nil {
			return netip.Addr{}, err
		}

		result, err := tx.ExecContext(ctx, insert, candidate.String(), familyOf(candidate), ownerID, now)
		if err != nil {
			return netip.Addr{}, fmt.Errorf("recording the allocation: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return netip.Addr{}, fmt.Errorf("recording the allocation: %w", err)
		}
		if affected == 1 {
			return candidate, nil
		}

		// Another transaction claimed it first. The insert blocked until that
		// transaction committed, so re-reading now sees its address and
		// everything else committed in the meantime — which is what stops the
		// retry walking upwards one collision at a time.
		refreshed, err := a.takenIn(ctx, tx, pool)
		if err != nil {
			return netip.Addr{}, err
		}
		refreshed[candidate] = struct{}{}
		taken = refreshed
	}

	return netip.Addr{}, fmt.Errorf(
		"could not claim an address in %s after %d attempts; another process may be allocating rapidly",
		pool, maxAllocationAttempts)
}

// pickCandidate chooses an address to attempt.
func (a *Allocator) pickCandidate(
	pool netip.Prefix, taken map[netip.Addr]struct{}, scatter bool,
) (netip.Addr, error) {
	limit := 1
	if scatter {
		limit = scatterWindow
	}

	free := a.freeAddresses(pool, taken, limit)
	if len(free) == 0 {
		return netip.Addr{}, fmt.Errorf("%w: %s", ErrPoolExhausted, pool)
	}
	if !scatter {
		return free[0], nil
	}

	// Contention avoidance only. This is not a security decision: an overlay
	// address is distributed to every authorised peer, so there is nothing
	// secret about which one a device receives.
	return free[rand.IntN(len(free))], nil //nolint:gosec // G404: not security-sensitive
}

// freeAddresses returns up to limit usable addresses in the pool, lowest
// first, skipping those already taken.
func (a *Allocator) freeAddresses(
	pool netip.Prefix, taken map[netip.Addr]struct{}, limit int,
) []netip.Addr {
	free := make([]netip.Addr, 0, limit)

	// Start past the network address, which is never handed out.
	for addr := pool.Masked().Addr().Next(); addr.IsValid() && pool.Contains(addr); addr = addr.Next() {
		// The IPv4 broadcast address is not usable as a host address.
		if addr.Is4() && addr == a.v4Broadcast {
			continue
		}
		if _, used := taken[addr]; used {
			continue
		}
		if free = append(free, addr); len(free) == limit {
			break
		}
	}
	return free
}

// takenIn reads the addresses already allocated within a pool.
func (a *Allocator) takenIn(ctx context.Context, q queryer, pool netip.Prefix) (map[netip.Addr]struct{}, error) {
	rows, err := q.QueryContext(ctx,
		a.db.Rebind(`SELECT address FROM ip_allocations WHERE family = ?`), familyOfPrefix(pool))
	if err != nil {
		return nil, fmt.Errorf("reading existing allocations: %w", err)
	}
	defer rows.Close()

	taken := make(map[netip.Addr]struct{})
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, fmt.Errorf("reading existing allocations: %w", err)
		}
		addr, err := netip.ParseAddr(text)
		if err != nil {
			// A row that will not parse cannot be reasoned about, and treating
			// it as free risks issuing a duplicate. Refuse instead.
			return nil, fmt.Errorf("allocation table holds an unparseable address %q: %w", text, err)
		}
		// Addresses outside the current pool are ignored: an operator may have
		// re-addressed the network, and stale rows must not block allocation.
		if pool.Contains(addr) {
			taken[addr] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading existing allocations: %w", err)
	}
	return taken, nil
}

// Release frees every address held by ownerID and reports how many were
// released. It is safe to call for an owner that holds none.
func (a *Allocator) Release(ctx context.Context, tx *sql.Tx, ownerID string) (int, error) {
	if tx == nil {
		return 0, errors.New("network: releasing requires a transaction")
	}
	result, err := tx.ExecContext(ctx,
		a.db.Rebind(`DELETE FROM ip_allocations WHERE owner_id = ?`), ownerID)
	if err != nil {
		return 0, fmt.Errorf("releasing the addresses of %s: %w", ownerID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("releasing the addresses of %s: %w", ownerID, err)
	}
	return int(affected), nil
}

// AddressesOf returns the addresses currently held by an owner. The zero
// Assignment means the owner holds none.
func (a *Allocator) AddressesOf(ctx context.Context, ownerID string) (Assignment, error) {
	return a.addressesOf(ctx, a.db, ownerID)
}

func (a *Allocator) addressesOf(ctx context.Context, q queryer, ownerID string) (Assignment, error) {
	rows, err := q.QueryContext(ctx,
		a.db.Rebind(`SELECT address FROM ip_allocations WHERE owner_id = ?`), ownerID)
	if err != nil {
		return Assignment{}, fmt.Errorf("reading the addresses of %s: %w", ownerID, err)
	}
	defer rows.Close()

	var assignment Assignment
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return Assignment{}, fmt.Errorf("reading the addresses of %s: %w", ownerID, err)
		}
		addr, err := netip.ParseAddr(text)
		if err != nil {
			return Assignment{}, fmt.Errorf("allocation table holds an unparseable address %q: %w", text, err)
		}
		if addr.Is4() {
			assignment.IPv4 = addr
		} else {
			assignment.IPv6 = addr
		}
	}
	if err := rows.Err(); err != nil {
		return Assignment{}, fmt.Errorf("reading the addresses of %s: %w", ownerID, err)
	}
	return assignment, nil
}

// queryer is the read subset shared by *sql.DB and *sql.Tx, so the same code
// serves both a standalone read and one inside a caller's transaction.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// familyOf reports 4 or 6 for an address.
func familyOf(addr netip.Addr) int {
	if addr.Is4() {
		return 4
	}
	return 6
}

// familyOfPrefix reports 4 or 6 for a prefix.
func familyOfPrefix(p netip.Prefix) int { return familyOf(p.Addr()) }

// lastAddr returns the highest address in a prefix — the broadcast address for
// IPv4.
func lastAddr(p netip.Prefix) netip.Addr {
	bytes := p.Masked().Addr().AsSlice()

	hostBits := p.Addr().BitLen() - p.Bits()
	for i := len(bytes) - 1; i >= 0 && hostBits > 0; i-- {
		n := min(8, hostBits)
		bytes[i] |= byte(0xff) >> (8 - n)
		hostBits -= n
	}

	addr, _ := netip.AddrFromSlice(bytes)
	return addr
}
