// Package storagetest opens databases for tests, against whichever backend the
// environment selects.
//
// It exists because Headnet supports two backends and, until now, only one of
// them was ever executed. The PostgreSQL migrations were written, shipped and
// entirely unverified — a syntax error in one would have reached an operator
// before it reached a test.
//
// By default tests run against an isolated in-memory SQLite database, exactly
// as before. When HEADNET_TEST_POSTGRES_DSN is set, the same tests run against
// PostgreSQL instead, each in its own schema. CI sets it for one job, so both
// dialects are exercised on every change.
//
// Running against PostgreSQL also gives the concurrency tests something SQLite
// cannot: a real connection pool. SQLite is held to a single connection, so
// transactions there serialise and never actually contend.
package storagetest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/CauaMora1s/Headnet/internal/storage"
	"github.com/CauaMora1s/Headnet/packages/shared"
)

// PostgresDSNEnv names the variable that switches tests to PostgreSQL.
const PostgresDSNEnv = "HEADNET_TEST_POSTGRES_DSN"

// safeIdentifier guards the generated schema name. The name comes from our own
// CSPRNG over a fixed alphabet, so this can never fail in practice — it is here
// so that a future change to the generator cannot quietly turn schema creation
// into string-concatenated SQL with attacker-shaped input.
var safeIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,48}$`)

// postgresDSN returns the configured PostgreSQL DSN, or "" for SQLite.
func postgresDSN() string { return strings.TrimSpace(os.Getenv(PostgresDSNEnv)) }

// Backend reports which backend tests will use.
func Backend() storage.Driver {
	if postgresDSN() != "" {
		return storage.DriverPostgres
	}
	return storage.DriverSQLite
}

// IsPostgres reports whether tests are running against PostgreSQL.
func IsPostgres() bool { return Backend() == storage.DriverPostgres }

// SkipUnlessSQLite skips a test that asserts on SQLite-specific behaviour,
// such as a pragma or a sqlite_master query.
func SkipUnlessSQLite(t *testing.T) {
	t.Helper()
	if IsPostgres() {
		t.Skip("this test asserts SQLite-specific behaviour")
	}
}

// Open returns a migrated, isolated database, closed automatically when the
// test finishes.
func Open(t *testing.T) *storage.DB {
	t.Helper()
	if dsn := postgresDSN(); dsn != "" {
		return openPostgres(t, dsn)
	}
	return openSQLite(t)
}

func openSQLite(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(t.Context(), storage.Options{
		Driver: storage.DriverSQLite,
		Path:   storage.MemoryPath,
	})
	if err != nil {
		t.Fatalf("opening the sqlite database failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := storage.Migrate(t.Context(), db, nil); err != nil {
		t.Fatalf("migrating failed: %v", err)
	}
	return db
}

// openPostgres gives each test its own schema, so tests stay isolated from one
// another without needing a database per test.
func openPostgres(t *testing.T, baseDSN string) *storage.DB {
	t.Helper()

	schema := "hn_" + strings.ToLower(shared.NewID(""))[:20]
	if !safeIdentifier.MatchString(schema) {
		t.Fatalf("generated an unusable schema name %q", schema)
	}

	// A separate handle on the default schema, used only to create and drop
	// the test schema.
	admin, err := storage.Open(context.Background(), storage.Options{
		Driver: storage.DriverPostgres, DSN: baseDSN, MaxOpenConns: 2, MaxIdleConns: 1,
	})
	if err != nil {
		t.Fatalf("connecting to postgres failed: %v", err)
	}
	if _, err := admin.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("creating the schema %s failed: %v", schema, err)
	}

	scoped, err := withSearchPath(baseDSN, schema)
	if err != nil {
		admin.Close()
		t.Fatalf("building the scoped DSN failed: %v", err)
	}

	db, err := storage.Open(context.Background(), storage.Options{
		Driver: storage.DriverPostgres, DSN: scoped, MaxOpenConns: 10, MaxIdleConns: 5,
	})
	if err != nil {
		admin.Close()
		t.Fatalf("connecting to the schema %s failed: %v", schema, err)
	}

	t.Cleanup(func() {
		db.Close()
		// Dropped with a background context: the test's own context is already
		// cancelled by the time cleanup runs.
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Logf("dropping the schema %s failed, leaving it behind: %v", schema, err)
		}
		admin.Close()
	})

	if _, err := storage.Migrate(context.Background(), db, nil); err != nil {
		t.Fatalf("migrating failed: %v", err)
	}
	return db
}

// withSearchPath returns the DSN with every connection pinned to one schema.
//
// The search_path travels in the connection string rather than being set with
// a statement, because a pooled connection that skipped the statement would
// silently read and write the wrong schema.
func withSearchPath(dsn, schema string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parsing the DSN: %w", err)
	}
	q := u.Query()
	q.Set("options", "-c search_path="+schema)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
