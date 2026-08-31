// Package devices is the device inventory: enrolment, the setup keys that
// authorise it, and revocation.
//
// Setup keys live here rather than in internal/auth because redeeming one
// creates a device in the same transaction. Splitting them across packages
// would split that transaction's reasoning across packages too.
//
// Three properties this package is responsible for:
//
//   - A device record holds a WireGuard *public* key and nothing that could
//     decrypt traffic. There is no field for a private key, and no API shape
//     with anywhere to put one. That is what makes a stolen database survivable.
//   - A setup key is stored only as a SHA-256 hash and shown exactly once, so
//     reading the table yields nothing that can enrol anything.
//   - Revocation is decided by the database, not by a read-then-write check in
//     Go. Single-use means single-use even when two enrolments race.
//
// Address allocation is delegated to internal/network and always runs inside
// the caller's transaction, so a device and its address are created — or
// discarded — together.
package devices

import (
	"context"
	"database/sql"
	"errors"

	"github.com/CauaMora1s/Headnet/internal/network"
	"github.com/CauaMora1s/Headnet/internal/storage"
)

// Queryer is the read surface shared by *sql.DB and *sql.Tx, so the same query
// serves a standalone read and one inside a caller's transaction.
type Queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Store reads and writes devices and setup keys.
type Store struct {
	db        *storage.DB
	allocator *network.Allocator
}

// NewStore builds a store over an open database.
//
// The allocator is required: a device without an address is not on the
// network, and making it optional would allow one to be created.
func NewStore(db *storage.DB, allocator *network.Allocator) (*Store, error) {
	if db == nil {
		return nil, errors.New("devices: a database handle is required")
	}
	if allocator == nil {
		return nil, errors.New("devices: an address allocator is required")
	}
	return &Store{db: db, allocator: allocator}, nil
}
