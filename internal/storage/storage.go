// Package storage opens and configures the control-plane database.
//
// Headnet supports two backends deliberately:
//
//   - SQLite is the default. A self-hosted control plane on a Raspberry Pi or
//     a NAS should not require a database server, and the whole state fits in
//     one file that an operator can copy as a backup.
//   - PostgreSQL is for larger or highly-available deployments.
//
// Both are reached through database/sql so that the repositories written in
// later phases stay backend-agnostic. Where the two dialects genuinely differ
// — placeholder syntax, timestamp types — the difference is confined to this
// package.
//
// The SQLite driver is modernc.org/sqlite, a pure-Go translation rather than a
// cgo binding. That keeps CGO_ENABLED=0 builds and cross-compilation working,
// which is what lets one CI job produce static binaries for six platforms.
// See docs/architecture/decisions/ADR-0005-database-sqlite-postgres.md.
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/CauaMora1s/Headnet/packages/shared"

	// Registered as "sqlite". Pure Go, so no cgo toolchain is required.
	_ "modernc.org/sqlite"

	// Registered as "pgx". The stdlib shim lets pgx serve database/sql.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Driver names a supported backend.
type Driver string

const (
	// DriverSQLite is the single-file default.
	DriverSQLite Driver = "sqlite"
	// DriverPostgres is the networked backend.
	DriverPostgres Driver = "postgres"
)

// MemoryPath opens a private, in-process database. It exists for tests; a
// server configured with it would lose every device on restart.
const MemoryPath = ":memory:"

// Options describes how to reach the database.
type Options struct {
	// Driver selects the backend.
	Driver Driver
	// Path is the SQLite file, or MemoryPath. Ignored for postgres.
	Path string
	// DSN is the PostgreSQL connection string. Ignored for sqlite.
	DSN string
	// MaxOpenConns caps concurrent connections. It is ignored for SQLite,
	// which is always held to one; see Open.
	MaxOpenConns int
	// MaxIdleConns caps pooled idle connections.
	MaxIdleConns int
	// ConnMaxLifetime retires connections after this long. Zero means never.
	ConnMaxLifetime time.Duration
}

// DB is a database handle that remembers which dialect it speaks.
type DB struct {
	*sql.DB
	driver Driver
}

// Driver reports the backend in use, so callers that must emit
// dialect-specific SQL can branch on it explicitly rather than guessing.
func (db *DB) Driver() Driver { return db.driver }

// Open connects to the database and verifies the connection.
//
// It does not run migrations; call Migrate separately so that a deployment can
// choose to migrate in a separate step from serving traffic.
func Open(ctx context.Context, opts Options) (*DB, error) {
	switch opts.Driver {
	case DriverSQLite:
		return openSQLite(ctx, opts)
	case DriverPostgres:
		return openPostgres(ctx, opts)
	case "":
		return nil, fmt.Errorf("storage: no driver configured (expected %s or %s)", DriverSQLite, DriverPostgres)
	default:
		return nil, fmt.Errorf("storage: unsupported driver %q", opts.Driver)
	}
}

func openSQLite(ctx context.Context, opts Options) (*DB, error) {
	if strings.TrimSpace(opts.Path) == "" {
		return nil, fmt.Errorf("storage: sqlite requires a database path")
	}

	dsn, err := sqliteDSN(opts.Path)
	if err != nil {
		return nil, err
	}

	handle, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: opening the sqlite database: %w", err)
	}

	// SQLite serialises writers. Holding the pool to a single connection
	// removes "database is locked" failures entirely, at the cost of
	// serialising reads too. For the deployment size SQLite is aimed at —
	// a household or a small team — that trade is the right one, and the
	// alternative (a separate read-only pool) is deferred to Phase 12.
	handle.SetMaxOpenConns(1)
	handle.SetMaxIdleConns(1)
	// A connection must not be retired underneath an in-memory database, or
	// the schema would vanish with it.
	handle.SetConnMaxLifetime(0)

	if err := ping(ctx, handle); err != nil {
		handle.Close()
		return nil, fmt.Errorf("storage: connecting to the sqlite database at %s: %w", opts.Path, err)
	}
	return &DB{DB: handle, driver: DriverSQLite}, nil
}

// sqliteDSN builds the connection string, creating the parent directory when
// the database lives on disk.
//
// The pragmas are not tuning preferences; each one prevents a specific failure:
//
//   - journal_mode(WAL) lets a reader run while a write is in flight, which
//     keeps /ready responsive during a burst of device check-ins.
//   - busy_timeout waits for a lock instead of failing instantly.
//   - foreign_keys(1) enforces referential integrity, which SQLite otherwise
//     leaves off by default — without it, deleting a user would silently
//     orphan its devices.
//   - synchronous(NORMAL) is the recommended durability level under WAL.
func sqliteDSN(path string) (string, error) {
	const pragmas = "_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)"

	if path == MemoryPath {
		// A private in-memory database, isolated from any other opened in the
		// same process so that parallel tests cannot see each other's schema.
		//
		// The name is drawn from the CSPRNG rather than from a timestamp:
		// Windows' wall clock is coarse enough that two opens in quick
		// succession would otherwise land on the same name and silently share
		// one database.
		return "file:headnet_mem_" + shared.NewID("") + "?mode=memory&cache=shared&" + pragmas, nil
	}

	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return "", fmt.Errorf("storage: creating the database directory %s: %w", dir, err)
		}
	}
	return "file:" + filepath.ToSlash(path) + "?" + pragmas, nil
}

func openPostgres(ctx context.Context, opts Options) (*DB, error) {
	if strings.TrimSpace(opts.DSN) == "" {
		return nil, fmt.Errorf("storage: postgres requires a connection string")
	}

	handle, err := sql.Open("pgx", opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("storage: opening the postgres database: %w", err)
	}

	if opts.MaxOpenConns > 0 {
		handle.SetMaxOpenConns(opts.MaxOpenConns)
	}
	if opts.MaxIdleConns >= 0 {
		handle.SetMaxIdleConns(opts.MaxIdleConns)
	}
	handle.SetConnMaxLifetime(opts.ConnMaxLifetime)

	if err := ping(ctx, handle); err != nil {
		handle.Close()
		// The DSN carries a password, so it must never reach an error message
		// that will end up in a log or a bug report.
		return nil, fmt.Errorf("storage: connecting to the postgres database: %w", err)
	}
	return &DB{DB: handle, driver: DriverPostgres}, nil
}

// ping verifies the connection, bounded so that an unreachable database fails
// start-up quickly rather than hanging the process.
func ping(ctx context.Context, handle *sql.DB) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return handle.PingContext(ctx)
}

// Rebind converts a query written with "?" placeholders into the dialect of
// this handle. Repositories write one query; this makes it run on both
// backends.
func (db *DB) Rebind(query string) string {
	if db.driver != DriverPostgres {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	for i := range len(query) {
		if query[i] != '?' {
			b.WriteByte(query[i])
			continue
		}
		n++
		b.WriteByte('$')
		b.WriteString(strconv.Itoa(n))
	}
	return b.String()
}

// InTx runs fn inside a transaction, committing on success and rolling back on
// any error or panic.
//
// The rollback is deliberately unconditional in the deferred function: a panic
// midway through a multi-statement change must not leave a transaction open,
// holding locks until the connection is reaped.
func (db *DB) InTx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning a transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("committing the transaction: %w", err)
	}
	return nil
}
