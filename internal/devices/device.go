package devices

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/CauaMora1s/Headnet/internal/auth"
	"github.com/CauaMora1s/Headnet/internal/storage"
	"github.com/CauaMora1s/Headnet/packages/shared"
)

// DeviceIDPrefix marks a device identifier in logs and audit records.
const DeviceIDPrefix = "dev"

// wireGuardKeyLength is the size of a Curve25519 key in bytes. A WireGuard
// public key is exactly this, base64-encoded.
const wireGuardKeyLength = 32

// maxNameLength and maxMetadataLength bound device-supplied strings. A device
// is not trusted, so everything it sends is bounded before it is stored.
const (
	maxNameLength     = 64
	maxMetadataLength = 128
)

// Device errors.
var (
	ErrDeviceNotFound = errors.New("no such device")
	// ErrPublicKeyTaken means the key is already registered — including to a
	// revoked device, whose key stays blocked permanently.
	ErrPublicKeyTaken = errors.New("that public key is already registered")
	// ErrInvalidPublicKey means the value is not a WireGuard public key.
	ErrInvalidPublicKey = errors.New("that is not a valid WireGuard public key")
	// ErrInvalidName means the device name was empty.
	ErrInvalidName = errors.New("the device needs a name")
	// ErrDeviceRevoked means the device exists but has been revoked.
	ErrDeviceRevoked = errors.New("the device has been revoked")
)

// Device is a machine on the network.
//
// It holds a public key and an address. It holds nothing that could decrypt
// traffic, and there is deliberately no field in which such a thing could be
// put.
type Device struct {
	ID     string
	UserID string
	Name   string
	// PublicKey is the device's WireGuard public key, base64-encoded. The
	// private half never leaves the device that generated it.
	PublicKey string
	OS        string
	Hostname  string
	// IPv4 and IPv6 are the addresses allocated to this device. IPv6 is
	// invalid on an IPv4-only deployment.
	IPv4 netip.Addr
	IPv6 netip.Addr
	// EnrolledWith is the setup key this device was enrolled with, if any. It
	// survives the key's deletion so the audit trail is not lost with it.
	EnrolledWith string
	CreatedAt    time.Time
	LastSeenAt   *time.Time
	// RevokedAt is nil while the device is active.
	RevokedAt *time.Time
}

// Revoked reports whether the device has been revoked.
func (d *Device) Revoked() bool { return d.RevokedAt != nil }

// Addrs returns the device's addresses, IPv4 first.
func (d *Device) Addrs() []netip.Addr {
	out := make([]netip.Addr, 0, 2)
	if d.IPv4.IsValid() {
		out = append(out, d.IPv4)
	}
	if d.IPv6.IsValid() {
		out = append(out, d.IPv6)
	}
	return out
}

// ValidatePublicKey checks that a value is a WireGuard public key.
//
// It can only check the shape: a public and a private key are both 32 random
// bytes, and nothing about the encoding distinguishes them. That is precisely
// why the API has no field for a private key — the server cannot tell you that
// you sent the wrong one, so the only safe design is one where sending it is
// impossible.
func ValidatePublicKey(key string) error {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return fmt.Errorf("%w: it is empty", ErrInvalidPublicKey)
	}

	raw, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return fmt.Errorf("%w: it is not valid base64", ErrInvalidPublicKey)
	}
	if len(raw) != wireGuardKeyLength {
		return fmt.Errorf("%w: it decodes to %d bytes, want %d",
			ErrInvalidPublicKey, len(raw), wireGuardKeyLength)
	}

	// An all-zero key is what an uninitialised buffer looks like. It is not a
	// valid Curve25519 point, and accepting it would mean silently enrolling a
	// device whose key generation failed.
	zero := true
	for _, b := range raw {
		if b != 0 {
			zero = false
			break
		}
	}
	if zero {
		return fmt.Errorf("%w: it is all zeroes, which suggests key generation failed", ErrInvalidPublicKey)
	}
	return nil
}

// NewDevice describes a device to register.
type NewDevice struct {
	// Name is what a human calls this machine.
	Name string
	// PublicKey is the device's WireGuard public key.
	PublicKey string
	// OS and Hostname are reported by the device for display. They are
	// untrusted and are bounded before storage.
	OS       string
	Hostname string
}

// Scope limits which devices an operation may see or touch.
//
// It is passed into the query rather than checked afterwards in a handler,
// because a filter that lives in the handler is a filter a future endpoint can
// forget to apply.
type Scope struct {
	// UserID limits the operation to one owner. Ignored when All is set.
	UserID string
	// All lifts the limit. Only an administrator gets this.
	All bool
}

// ScopeFor builds the scope for a user.
func ScopeFor(userID string, admin bool) Scope { return Scope{UserID: userID, All: admin} }

const deviceColumns = `id, user_id, name, public_key, os, hostname, ipv4, ipv6,
	enrolled_with, created_at, last_seen_at, revoked_at`

// Register adds a device owned by a user and allocates its addresses.
//
// The device row and its address allocation are written in one transaction, so
// a failure at either step leaves neither behind and no address is leaked.
func (s *Store) Register(ctx context.Context, userID string, in NewDevice, now time.Time) (*Device, error) {
	if userID == "" {
		return nil, errors.New("devices: an owning user is required")
	}
	return s.create(ctx, userID, in, "", "", now)
}

// Enroll redeems a setup key and registers the device it authorises.
//
// The key is consumed and the device created in one transaction. If device
// creation fails — a duplicate public key, say — the redemption is rolled back
// with it, so a failed enrolment does not silently spend a single-use key.
// The returned device token is shown once; only its hash is persisted.
func (s *Store) Enroll(ctx context.Context, token string, in NewDevice, now time.Time) (*Device, string, error) {
	key, err := s.SetupKeyByToken(ctx, nil, token)
	if err != nil {
		return nil, "", err
	}
	if reason := key.Redeemable(now); reason != nil {
		return nil, "", reason
	}
	secret, err := auth.NewToken(32)
	if err != nil {
		return nil, "", err
	}
	secret = deviceTokenPrefix + secret
	device, err := s.create(ctx, key.CreatedBy, in, key.ID, auth.HashToken(secret), now)
	if err != nil {
		return nil, "", err
	}
	return device, secret, nil
}

// create is the shared body of Register and Enroll.
func (s *Store) create(
	ctx context.Context, userID string, in NewDevice, setupKeyID, tokenHash string, now time.Time,
) (*Device, error) {
	name := truncate(strings.TrimSpace(in.Name), maxNameLength)
	if name == "" {
		return nil, ErrInvalidName
	}
	publicKey := strings.TrimSpace(in.PublicKey)
	if err := ValidatePublicKey(publicKey); err != nil {
		return nil, err
	}

	device := &Device{
		ID:           shared.NewID(DeviceIDPrefix),
		UserID:       userID,
		Name:         name,
		PublicKey:    publicKey,
		OS:           truncate(strings.TrimSpace(in.OS), maxMetadataLength),
		Hostname:     truncate(strings.TrimSpace(in.Hostname), maxMetadataLength),
		EnrolledWith: setupKeyID,
		CreatedAt:    now,
	}

	err := s.db.InTx(ctx, func(tx *sql.Tx) error {
		// Consume the key first: if the device turns out to be unregisterable,
		// rolling back must un-spend the use.
		if setupKeyID != "" {
			key, err := s.setupKeyByIDTx(ctx, tx, setupKeyID)
			if err != nil {
				return err
			}
			if err := s.consumeSetupKey(ctx, tx, key, now); err != nil {
				return err
			}
		}

		assignment, err := s.allocator.Allocate(ctx, tx, device.ID, now)
		if err != nil {
			return err
		}
		device.IPv4, device.IPv6 = assignment.IPv4, assignment.IPv6

		_, err = tx.ExecContext(ctx, s.db.Rebind(`
			INSERT INTO devices (`+deviceColumns+`)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL)`),
			device.ID, device.UserID, device.Name, device.PublicKey, device.OS, device.Hostname,
			addrOrNil(device.IPv4), addrOrNil(device.IPv6), nullableID(device.EnrolledWith),
			device.CreatedAt,
		)
		if err != nil {
			// Let the unique index decide rather than checking first, which
			// would race with a concurrent registration of the same key.
			if storage.IsUniqueViolation(err) {
				return fmt.Errorf("%w: %s", ErrPublicKeyTaken, publicKey)
			}
			return fmt.Errorf("creating the device: %w", err)
		}
		if tokenHash != "" {
			if _, err := tx.ExecContext(ctx, s.db.Rebind(`
				INSERT INTO device_tokens (device_id, token_hash, created_at) VALUES (?, ?, ?)`),
				device.ID, tokenHash, now); err != nil {
				return fmt.Errorf("creating the device credential: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return device, nil
}

// ByID returns a device, subject to scope.
func (s *Store) ByID(ctx context.Context, id string, scope Scope) (*Device, error) {
	query := `SELECT ` + deviceColumns + ` FROM devices WHERE id = ?`
	args := []any{id}
	if !scope.All {
		query += ` AND user_id = ?`
		args = append(args, scope.UserID)
	}

	rows, err := s.db.QueryContext(ctx, s.db.Rebind(query), args...)
	if err != nil {
		return nil, fmt.Errorf("reading the device: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("reading the device: %w", err)
		}
		// A device that exists but belongs to someone else is reported as
		// missing rather than forbidden. "You may not see this" confirms it
		// exists, which is more than the caller is entitled to know.
		return nil, ErrDeviceNotFound
	}
	return scanDevice(rows)
}

// List returns devices, newest first, subject to scope.
func (s *Store) List(ctx context.Context, scope Scope) ([]Device, error) {
	query := `SELECT ` + deviceColumns + ` FROM devices`
	var args []any
	if !scope.All {
		query += ` WHERE user_id = ?`
		args = append(args, scope.UserID)
	}
	query += ` ORDER BY created_at DESC, id DESC`

	rows, err := s.db.QueryContext(ctx, s.db.Rebind(query), args...)
	if err != nil {
		return nil, fmt.Errorf("listing devices: %w", err)
	}
	defer rows.Close()

	var out []Device
	for rows.Next() {
		device, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *device)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing devices: %w", err)
	}
	return out, nil
}

// Revoke ends a device's membership and releases its addresses.
//
// The device row is kept, marked revoked, so the audit trail survives. Its
// public key therefore stays blocked, which is the intended behaviour: a
// revoked key must never work again.
//
// Address release and the revocation are one transaction, so the pool cannot
// end up holding an address for a device that is no longer on the network.
func (s *Store) Revoke(ctx context.Context, id string, scope Scope, now time.Time) error {
	device, err := s.ByID(ctx, id, scope)
	if err != nil {
		return err
	}
	if device.Revoked() {
		return ErrDeviceRevoked
	}

	return s.db.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.allocator.Release(ctx, tx, device.ID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, s.db.Rebind(
			`UPDATE devices SET revoked_at = ?, ipv4 = NULL, ipv6 = NULL
			 WHERE id = ? AND revoked_at IS NULL`), now, device.ID)
		if err != nil {
			return fmt.Errorf("revoking the device: %w", err)
		}
		if affected, err := result.RowsAffected(); err == nil && affected == 0 {
			// Another request revoked it between the read and the update.
			return ErrDeviceRevoked
		}
		if _, err := tx.ExecContext(ctx, s.db.Rebind(
			`DELETE FROM device_tokens WHERE device_id = ?`), device.ID); err != nil {
			return fmt.Errorf("revoking the device credential: %w", err)
		}
		return nil
	})
}

// RecordSeen stamps a device as having checked in.
func (s *Store) RecordSeen(ctx context.Context, id string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, s.db.Rebind(
		`UPDATE devices SET last_seen_at = ? WHERE id = ? AND revoked_at IS NULL`), now, id)
	if err != nil {
		return fmt.Errorf("recording the device check-in: %w", err)
	}
	return nil
}

// Count returns how many devices exist within scope.
func (s *Store) Count(ctx context.Context, scope Scope) (int, error) {
	query := `SELECT COUNT(*) FROM devices`
	var args []any
	if !scope.All {
		query += ` WHERE user_id = ?`
		args = append(args, scope.UserID)
	}

	var n int
	if err := s.db.QueryRowContext(ctx, s.db.Rebind(query), args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting devices: %w", err)
	}
	return n, nil
}

func scanDevice(row rowScanner) (*Device, error) {
	var (
		device       Device
		ipv4, ipv6   sql.NullString
		enrolledWith sql.NullString
		lastSeen     sql.NullTime
		revokedAt    sql.NullTime
	)
	err := row.Scan(&device.ID, &device.UserID, &device.Name, &device.PublicKey,
		&device.OS, &device.Hostname, &ipv4, &ipv6, &enrolledWith,
		&device.CreatedAt, &lastSeen, &revokedAt)
	if err != nil {
		return nil, fmt.Errorf("reading the device: %w", err)
	}

	if ipv4.Valid {
		device.IPv4, _ = netip.ParseAddr(ipv4.String)
	}
	if ipv6.Valid {
		device.IPv6, _ = netip.ParseAddr(ipv6.String)
	}
	device.EnrolledWith = enrolledWith.String
	if lastSeen.Valid {
		t := lastSeen.Time
		device.LastSeenAt = &t
	}
	if revokedAt.Valid {
		t := revokedAt.Time
		device.RevokedAt = &t
	}
	return &device, nil
}

// addrOrNil renders an address for storage, or NULL when unset.
func addrOrNil(addr netip.Addr) any {
	if !addr.IsValid() {
		return nil
	}
	return addr.String()
}

// nullableID renders an optional foreign key.
func nullableID(id string) any {
	if id == "" {
		return nil
	}
	return id
}
