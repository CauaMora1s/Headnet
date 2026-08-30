package storage_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/CauaMora1s/Headnet/internal/storage"
)

func TestEveryDialectShipsTheSameMigrationHistory(t *testing.T) {
	t.Parallel()
	// A migration added for one backend and forgotten for the other is the
	// classic way a project quietly becomes single-backend.
	sqlite, err := storage.LoadMigrations(storage.DriverSQLite)
	if err != nil {
		t.Fatalf("loading the sqlite migrations failed: %v", err)
	}
	postgres, err := storage.LoadMigrations(storage.DriverPostgres)
	if err != nil {
		t.Fatalf("loading the postgres migrations failed: %v", err)
	}

	if len(sqlite) != len(postgres) {
		t.Fatalf("sqlite has %d migrations, postgres has %d", len(sqlite), len(postgres))
	}
	for i := range sqlite {
		if sqlite[i].Version != postgres[i].Version || sqlite[i].Name != postgres[i].Name {
			t.Errorf("migration %d differs: sqlite has %s, postgres has %s",
				i, sqlite[i].Filename(), postgres[i].Filename())
		}
	}
}

func TestMigrationsAreLoadedInOrderAndAreWellFormed(t *testing.T) {
	t.Parallel()
	migrations, err := storage.LoadMigrations(storage.DriverSQLite)
	if err != nil {
		t.Fatalf("LoadMigrations failed: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("no migrations were embedded")
	}
	for i, m := range migrations {
		if m.Version <= 0 {
			t.Errorf("migration %s has a non-positive version", m.Filename())
		}
		if i > 0 && m.Version <= migrations[i-1].Version {
			t.Errorf("migrations are not in ascending order: %s follows %s",
				m.Filename(), migrations[i-1].Filename())
		}
		if strings.TrimSpace(m.SQL) == "" {
			t.Errorf("migration %s is empty", m.Filename())
		}
		if len(m.Checksum) != 64 {
			t.Errorf("migration %s has a malformed checksum %q", m.Filename(), m.Checksum)
		}
	}
}

func TestLoadMigrationsRejectsAnUnknownDialect(t *testing.T) {
	t.Parallel()
	if _, err := storage.LoadMigrations("mysql"); err == nil {
		t.Fatal("LoadMigrations accepted a dialect with no embedded migrations")
	}
}

func TestMigrateAppliesEverythingOnAFreshDatabase(t *testing.T) {
	db := openBare(t)

	applied, err := storage.Migrate(t.Context(), db, nil)
	if err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	expected, err := storage.LoadMigrations(storage.DriverSQLite)
	if err != nil {
		t.Fatalf("LoadMigrations failed: %v", err)
	}
	if applied != len(expected) {
		t.Fatalf("Migrate applied %d migrations, want %d", applied, len(expected))
	}

	version, err := storage.SchemaVersion(t.Context(), db)
	if err != nil {
		t.Fatalf("SchemaVersion failed: %v", err)
	}
	if want := expected[len(expected)-1].Version; version != want {
		t.Fatalf("SchemaVersion = %d, want %d", version, want)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	// Every server start calls Migrate, so a second run must be a no-op
	// rather than an error or a duplicate application.
	db := openBare(t)

	if _, err := storage.Migrate(t.Context(), db, nil); err != nil {
		t.Fatalf("the first Migrate failed: %v", err)
	}
	applied, err := storage.Migrate(t.Context(), db, nil)
	if err != nil {
		t.Fatalf("the second Migrate failed: %v", err)
	}
	if applied != 0 {
		t.Fatalf("the second Migrate applied %d migrations, want 0", applied)
	}
}

func TestSchemaVersionIsZeroBeforeMigrating(t *testing.T) {
	db := openBare(t)
	// The ledger does not exist yet, so this must fail rather than silently
	// report a migrated database.
	if _, err := storage.SchemaVersion(t.Context(), db); err == nil {
		t.Fatal("SchemaVersion succeeded against an unmigrated database")
	}
}

func TestMigrateRecordsTheLedgerFaithfully(t *testing.T) {
	db := openMemory(t)

	applied, err := storage.AppliedMigrations(t.Context(), db)
	if err != nil {
		t.Fatalf("AppliedMigrations failed: %v", err)
	}
	available, err := storage.LoadMigrations(storage.DriverSQLite)
	if err != nil {
		t.Fatalf("LoadMigrations failed: %v", err)
	}
	if len(applied) != len(available) {
		t.Fatalf("the ledger has %d rows, want %d", len(applied), len(available))
	}
	for i := range applied {
		if applied[i].Version != available[i].Version {
			t.Errorf("ledger row %d: version = %d, want %d", i, applied[i].Version, available[i].Version)
		}
		if applied[i].Checksum != available[i].Checksum {
			t.Errorf("ledger row %d: checksum was not recorded faithfully", i)
		}
		if applied[i].AppliedAt.IsZero() {
			t.Errorf("ledger row %d: applied_at was not recorded", i)
		}
	}
}

func TestEditingAnAppliedMigrationIsRefused(t *testing.T) {
	// Editing an applied migration is how two deployments of the same version
	// end up with different schemas. The checksum in the ledger is what makes
	// that detectable rather than silent.
	db := openMemory(t)

	if err := corruptLedgerChecksum(t, db); err != nil {
		t.Fatalf("preparing the test failed: %v", err)
	}

	_, err := storage.Migrate(t.Context(), db, nil)
	if !errors.Is(err, storage.ErrChecksumMismatch) {
		t.Fatalf("Migrate error = %v, want it to wrap ErrChecksumMismatch", err)
	}
	if !strings.Contains(err.Error(), "create a new migration") {
		t.Fatalf("the error does not tell the operator what to do instead:\n%v", err)
	}
}

// corruptLedgerChecksum simulates a migration file that was edited after being
// applied, by rewriting the checksum the database recorded for it.
func corruptLedgerChecksum(t *testing.T, db *storage.DB) error {
	t.Helper()
	_, err := db.ExecContext(t.Context(),
		`UPDATE schema_migrations SET checksum = 'deadbeef' WHERE version = 1`)
	return err
}

func TestADatabaseFromANewerBuildIsRefused(t *testing.T) {
	// Running an old binary against a schema it has never seen would corrupt
	// data. Start-up has to stop instead.
	db := openMemory(t)

	_, err := db.ExecContext(t.Context(),
		`INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (9999, 'from_the_future', 'abc', ?)`,
		time.Now().UTC())
	if err != nil {
		t.Fatalf("preparing the test failed: %v", err)
	}

	_, err = storage.Migrate(t.Context(), db, nil)
	if !errors.Is(err, storage.ErrDatabaseAhead) {
		t.Fatalf("Migrate error = %v, want it to wrap ErrDatabaseAhead", err)
	}
	if !strings.Contains(err.Error(), "upgrade the server") {
		t.Fatalf("the error does not tell the operator how to recover:\n%v", err)
	}
}

func TestMigrateAcceptsANilLogger(t *testing.T) {
	// Migrate runs before the logger is necessarily wired up, so a nil logger
	// must not panic.
	db := openBare(t)
	if _, err := storage.Migrate(t.Context(), db, nil); err != nil {
		t.Fatalf("Migrate with a nil logger failed: %v", err)
	}
}

func TestMigrationFilenameRoundTrips(t *testing.T) {
	t.Parallel()
	m := storage.Migration{Version: 7, Name: "add_devices"}
	if got := m.Filename(); got != "0007_add_devices.sql" {
		t.Fatalf("Filename() = %q, want 0007_add_devices.sql", got)
	}
}

func TestInitialMigrationCreatesServerMetadata(t *testing.T) {
	db := openMemory(t)
	var name string
	err := db.QueryRowContext(t.Context(),
		`SELECT name FROM sqlite_master WHERE type='table' AND name='server_metadata'`).Scan(&name)
	if err != nil {
		t.Fatalf("server_metadata was not created by the initial migration: %v", err)
	}
}
