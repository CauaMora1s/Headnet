package storage_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/headnet/headnet/internal/storage"
)

// openMemory returns a migrated, isolated in-memory database.
func openMemory(t *testing.T) *storage.DB {
	t.Helper()
	db := openBare(t)
	if _, err := storage.Migrate(t.Context(), db, nil); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	return db
}

// openBare returns an unmigrated in-memory database.
func openBare(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(t.Context(), storage.Options{
		Driver: storage.DriverSQLite,
		Path:   storage.MemoryPath,
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenRejectsUnusableOptions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		opts storage.Options
	}{
		{"no driver", storage.Options{}},
		{"unknown driver", storage.Options{Driver: "mysql"}},
		{"sqlite without a path", storage.Options{Driver: storage.DriverSQLite}},
		{"sqlite with a blank path", storage.Options{Driver: storage.DriverSQLite, Path: "   "}},
		{"postgres without a dsn", storage.Options{Driver: storage.DriverPostgres}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db, err := storage.Open(t.Context(), tt.opts)
			if err == nil {
				db.Close()
				t.Fatal("Open accepted an unusable configuration")
			}
		})
	}
}

func TestOpenCreatesTheDatabaseDirectory(t *testing.T) {
	// A first-time operator should not have to mkdir before starting the
	// server for the first time.
	path := filepath.Join(t.TempDir(), "nested", "deeper", "headnet.db")

	db, err := storage.Open(t.Context(), storage.Options{
		Driver: storage.DriverSQLite,
		Path:   path,
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	if _, err := storage.Migrate(t.Context(), db, nil); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO server_metadata (key, value, created_at, updated_at) VALUES ('k','v',?,?)`,
		time.Now(), time.Now()); err != nil {
		t.Fatalf("the database is not usable after Open: %v", err)
	}
}

func TestSQLiteEnforcesForeignKeys(t *testing.T) {
	// SQLite leaves foreign keys off by default. Without the pragma, deleting
	// a user in a later phase would silently orphan its devices.
	db := openMemory(t)
	var enabled int
	if err := db.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&enabled); err != nil {
		t.Fatalf("reading the foreign_keys pragma failed: %v", err)
	}
	if enabled != 1 {
		t.Fatal("foreign key enforcement is off")
	}
}

func TestSQLiteUsesWriteAheadLogging(t *testing.T) {
	db := openMemory(t)
	var mode string
	if err := db.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("reading the journal_mode pragma failed: %v", err)
	}
	// An in-memory database cannot use WAL; on disk it must.
	onDisk, err := storage.Open(t.Context(), storage.Options{
		Driver: storage.DriverSQLite,
		Path:   filepath.Join(t.TempDir(), "headnet.db"),
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer onDisk.Close()

	if err := onDisk.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("reading the journal_mode pragma failed: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestDriverIsReported(t *testing.T) {
	t.Parallel()
	if got := openBare(t).Driver(); got != storage.DriverSQLite {
		t.Fatalf("Driver() = %q, want %q", got, storage.DriverSQLite)
	}
}

func TestInMemoryDatabasesAreIsolated(t *testing.T) {
	// Parallel tests must not be able to see each other's schema.
	first := openMemory(t)
	second := openBare(t)

	var name string
	err := second.QueryRowContext(t.Context(),
		`SELECT name FROM sqlite_master WHERE type='table' AND name='server_metadata'`).Scan(&name)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("a second in-memory database saw the first one's schema (err=%v, name=%q)", err, name)
	}
	_ = first
}

func TestRebind(t *testing.T) {
	t.Parallel()
	sqlite := openBare(t)

	const query = `SELECT * FROM t WHERE a = ? AND b = ?`
	if got := sqlite.Rebind(query); got != query {
		t.Fatalf("Rebind on sqlite = %q, want the query unchanged", got)
	}
}

func TestInTxCommitsOnSuccess(t *testing.T) {
	db := openMemory(t)
	err := db.InTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(),
			`INSERT INTO server_metadata (key, value, created_at, updated_at) VALUES ('committed','yes',?,?)`,
			time.Now(), time.Now())
		return err
	})
	if err != nil {
		t.Fatalf("InTx failed: %v", err)
	}
	if got, err := storage.GetMetadata(t.Context(), db, "committed"); err != nil || got != "yes" {
		t.Fatalf("value after commit = (%q, %v), want (\"yes\", nil)", got, err)
	}
}

func TestInTxRollsBackOnError(t *testing.T) {
	db := openMemory(t)
	sentinel := errors.New("business rule violated")

	err := db.InTx(t.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(),
			`INSERT INTO server_metadata (key, value, created_at, updated_at) VALUES ('rolled_back','yes',?,?)`,
			time.Now(), time.Now()); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("InTx error = %v, want the callback error to propagate", err)
	}
	if _, err := storage.GetMetadata(t.Context(), db, "rolled_back"); !errors.Is(err, storage.ErrMetadataNotFound) {
		t.Fatalf("the write survived a rolled-back transaction (err=%v)", err)
	}
}

func TestInTxRollsBackOnPanic(t *testing.T) {
	// A panic must not leave a transaction open holding locks until the
	// connection is reaped, which on a single-connection SQLite pool would
	// deadlock the whole server.
	db := openMemory(t)

	func() {
		defer func() {
			if recover() == nil {
				t.Error("InTx swallowed a panic; it must propagate after rolling back")
			}
		}()
		_ = db.InTx(t.Context(), func(tx *sql.Tx) error {
			_, _ = tx.ExecContext(t.Context(),
				`INSERT INTO server_metadata (key, value, created_at, updated_at) VALUES ('panicked','yes',?,?)`,
				time.Now(), time.Now())
			panic("boom")
		})
	}()

	if _, err := storage.GetMetadata(t.Context(), db, "panicked"); !errors.Is(err, storage.ErrMetadataNotFound) {
		t.Fatalf("the write survived a panicking transaction (err=%v)", err)
	}

	// The pool must still be usable afterwards.
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("the connection is unusable after a panic: %v", err)
	}
}

func TestMetadataRoundTrip(t *testing.T) {
	db := openMemory(t)
	now := time.Now().UTC()

	if err := storage.SetMetadata(t.Context(), db, "colour", "blue", now); err != nil {
		t.Fatalf("SetMetadata failed: %v", err)
	}
	if got, err := storage.GetMetadata(t.Context(), db, "colour"); err != nil || got != "blue" {
		t.Fatalf("GetMetadata = (%q, %v), want (\"blue\", nil)", got, err)
	}

	// A second write updates rather than failing on the primary key.
	if err := storage.SetMetadata(t.Context(), db, "colour", "green", now.Add(time.Minute)); err != nil {
		t.Fatalf("SetMetadata failed on update: %v", err)
	}
	if got, _ := storage.GetMetadata(t.Context(), db, "colour"); got != "green" {
		t.Fatalf("GetMetadata after update = %q, want \"green\"", got)
	}
}

func TestGetMetadataDistinguishesMissingKeys(t *testing.T) {
	db := openMemory(t)
	_, err := storage.GetMetadata(t.Context(), db, "never-set")
	if !errors.Is(err, storage.ErrMetadataNotFound) {
		t.Fatalf("error = %v, want it to wrap ErrMetadataNotFound", err)
	}
}

func TestInstanceIDIsGeneratedOnceAndPersisted(t *testing.T) {
	db := openMemory(t)
	now := time.Now().UTC()

	first, err := storage.InstanceID(t.Context(), db, now)
	if err != nil {
		t.Fatalf("InstanceID failed: %v", err)
	}
	if first == "" {
		t.Fatal("InstanceID returned an empty identity")
	}
	if !strings.HasPrefix(first, "net_") {
		t.Errorf("InstanceID = %q, want a net_ prefix", first)
	}

	second, err := storage.InstanceID(t.Context(), db, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("InstanceID failed on the second call: %v", err)
	}
	if second != first {
		t.Fatalf("InstanceID changed between calls: %q then %q", first, second)
	}
}

func TestInstanceIDRecordsWhenTheDatabaseWasInitialised(t *testing.T) {
	db := openMemory(t)
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)

	if _, err := storage.InstanceID(t.Context(), db, now); err != nil {
		t.Fatalf("InstanceID failed: %v", err)
	}
	got, err := storage.GetMetadata(t.Context(), db, storage.KeyInitialisedAt)
	if err != nil {
		t.Fatalf("reading %s failed: %v", storage.KeyInitialisedAt, err)
	}
	if got != "2026-08-29T12:00:00Z" {
		t.Fatalf("%s = %q, want the RFC 3339 initialisation time", storage.KeyInitialisedAt, got)
	}
}

func TestInstanceIDsDifferBetweenDeployments(t *testing.T) {
	// A client records the instance it enrolled against; two independently
	// created control planes must be distinguishable.
	a, err := storage.InstanceID(t.Context(), openMemory(t), time.Now())
	if err != nil {
		t.Fatalf("InstanceID failed: %v", err)
	}
	b, err := storage.InstanceID(t.Context(), openMemory(t), time.Now())
	if err != nil {
		t.Fatalf("InstanceID failed: %v", err)
	}
	if a == b {
		t.Fatalf("two separate deployments produced the same instance ID %q", a)
	}
}

func TestContextCancellationIsHonoured(t *testing.T) {
	db := openMemory(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := storage.GetMetadata(ctx, db, storage.KeyInstanceID); err == nil {
		t.Fatal("a cancelled context did not abort the query")
	}
}
