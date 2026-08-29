package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

// migrationsFS holds the schema history, embedded so that a single binary can
// migrate a database with no files alongside it.
//
//go:embed migrations/sqlite/*.sql migrations/postgres/*.sql
var migrationsFS embed.FS

// Sentinel errors that an operator, not a programmer, has to resolve.
var (
	// ErrChecksumMismatch means a migration file changed after it had already
	// been applied. Editing applied migrations is how schemas silently
	// diverge between deployments, so it is refused rather than reconciled.
	ErrChecksumMismatch = errors.New("an already-applied migration has been modified")

	// ErrDatabaseAhead means the database was migrated by a newer build. A
	// rollback that ran the old binary against the new schema would corrupt
	// data, so start-up stops here.
	ErrDatabaseAhead = errors.New("the database schema is newer than this build understands")
)

// Migration is one versioned schema change.
type Migration struct {
	// Version orders migrations and is unique within a dialect.
	Version int
	// Name is the human-readable part of the filename.
	Name string
	// SQL is the statement text.
	SQL string
	// Checksum is the SHA-256 of SQL, recorded so later runs can detect that
	// the file was edited after being applied.
	Checksum string
}

// Filename reconstructs the file this migration was loaded from.
func (m Migration) Filename() string {
	return fmt.Sprintf("%04d_%s.sql", m.Version, m.Name)
}

// AppliedMigration is a row of the migration ledger.
type AppliedMigration struct {
	Version   int
	Name      string
	Checksum  string
	AppliedAt time.Time
}

// Migrate brings the database up to the schema this build expects and reports
// how many migrations it applied.
//
// It is safe to call on every start-up: already-applied migrations are skipped,
// so running two servers against one database converges rather than conflicts.
func Migrate(ctx context.Context, db *DB, logger *slog.Logger) (int, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	available, err := LoadMigrations(db.Driver())
	if err != nil {
		return 0, err
	}
	if err := ensureLedger(ctx, db); err != nil {
		return 0, err
	}
	applied, err := AppliedMigrations(ctx, db)
	if err != nil {
		return 0, err
	}
	if err := verifyLedger(available, applied); err != nil {
		return 0, err
	}

	appliedVersions := make(map[int]struct{}, len(applied))
	for _, a := range applied {
		appliedVersions[a.Version] = struct{}{}
	}

	count := 0
	for _, m := range available {
		if _, done := appliedVersions[m.Version]; done {
			continue
		}
		start := time.Now()
		if err := applyOne(ctx, db, m); err != nil {
			return count, fmt.Errorf("applying migration %s: %w", m.Filename(), err)
		}
		count++
		logger.InfoContext(ctx, "applied database migration",
			"version", m.Version,
			"name", m.Name,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}
	return count, nil
}

// applyOne runs a migration and records it in the same transaction.
//
// Coupling the two is what makes the process crash-safe: a process killed
// midway either applied the change and recorded it, or did neither.
func applyOne(ctx context.Context, db *DB, m Migration) error {
	return db.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
			return err
		}
		query := db.Rebind(
			`INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (?, ?, ?, ?)`)
		_, err := tx.ExecContext(ctx, query, m.Version, m.Name, m.Checksum, time.Now().UTC())
		return err
	})
}

// ensureLedger creates the bookkeeping table.
//
// It is created here rather than as migration 0001 for an obvious reason: the
// migrator needs somewhere to record that 0001 ran.
func ensureLedger(ctx context.Context, db *DB) error {
	timestampType := "TIMESTAMP NOT NULL"
	if db.Driver() == DriverPostgres {
		timestampType = "TIMESTAMPTZ NOT NULL"
	}
	stmt := `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		checksum   TEXT NOT NULL,
		applied_at ` + timestampType + `
	)`
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("creating the schema_migrations table: %w", err)
	}
	return nil
}

// AppliedMigrations returns the ledger in version order.
func AppliedMigrations(ctx context.Context, db *DB) ([]AppliedMigration, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT version, name, checksum, applied_at FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("reading the migration ledger: %w", err)
	}
	defer rows.Close()

	var out []AppliedMigration
	for rows.Next() {
		var a AppliedMigration
		if err := rows.Scan(&a.Version, &a.Name, &a.Checksum, &a.AppliedAt); err != nil {
			return nil, fmt.Errorf("reading the migration ledger: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the migration ledger: %w", err)
	}
	return out, nil
}

// verifyLedger checks the recorded history against the embedded files before
// anything is applied.
func verifyLedger(available []Migration, applied []AppliedMigration) error {
	byVersion := make(map[int]Migration, len(available))
	for _, m := range available {
		byVersion[m.Version] = m
	}

	for _, a := range applied {
		m, known := byVersion[a.Version]
		if !known {
			// The database records a migration this build has never seen,
			// which means it was written by a newer server.
			return fmt.Errorf("%w: migration %d (%s) was applied on %s but is not present in this build; "+
				"upgrade the server, or restore the database from a backup taken before the upgrade",
				ErrDatabaseAhead, a.Version, a.Name, a.AppliedAt.Format(time.RFC3339))
		}
		if m.Checksum != a.Checksum {
			return fmt.Errorf("%w: migration %s no longer matches the version applied on %s "+
				"(recorded %s, found %s); create a new migration instead of editing an applied one",
				ErrChecksumMismatch, m.Filename(), a.AppliedAt.Format(time.RFC3339),
				short(a.Checksum), short(m.Checksum))
		}
	}
	return nil
}

func short(checksum string) string {
	if len(checksum) <= 12 {
		return checksum
	}
	return checksum[:12]
}

// LoadMigrations reads and validates the embedded migrations for a dialect,
// returning them in ascending version order.
func LoadMigrations(driver Driver) ([]Migration, error) {
	dir := path.Join("migrations", string(driver))
	entries, err := fs.ReadDir(migrationsFS, dir)
	if err != nil {
		return nil, fmt.Errorf("storage: no embedded migrations for driver %q: %w", driver, err)
	}

	out := make([]Migration, 0, len(entries))
	seen := make(map[int]string, len(entries))

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, name, err := parseMigrationName(entry.Name())
		if err != nil {
			return nil, err
		}
		if previous, dup := seen[version]; dup {
			return nil, fmt.Errorf("storage: migrations %s and %s share version %d",
				previous, entry.Name(), version)
		}
		seen[version] = entry.Name()

		body, err := fs.ReadFile(migrationsFS, path.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("storage: reading migration %s: %w", entry.Name(), err)
		}
		if len(strings.TrimSpace(string(body))) == 0 {
			return nil, fmt.Errorf("storage: migration %s is empty", entry.Name())
		}

		sum := sha256.Sum256(body)
		out = append(out, Migration{
			Version:  version,
			Name:     name,
			SQL:      string(body),
			Checksum: hex.EncodeToString(sum[:]),
		})
	}

	slices.SortFunc(out, func(a, b Migration) int { return a.Version - b.Version })
	return out, nil
}

// parseMigrationName splits "0001_init.sql" into 1 and "init".
func parseMigrationName(filename string) (int, string, error) {
	base := strings.TrimSuffix(filename, ".sql")
	prefix, name, ok := strings.Cut(base, "_")
	if !ok || name == "" {
		return 0, "", fmt.Errorf(
			"storage: migration %q is misnamed (expected NNNN_description.sql)", filename)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil || version <= 0 {
		return 0, "", fmt.Errorf(
			"storage: migration %q has no positive numeric version prefix", filename)
	}
	return version, name, nil
}

// SchemaVersion returns the highest applied migration version, or zero for an
// unmigrated database. Diagnostics and the readiness probe report it.
func SchemaVersion(ctx context.Context, db *DB) (int, error) {
	applied, err := AppliedMigrations(ctx, db)
	if err != nil {
		return 0, err
	}
	version := 0
	for _, a := range applied {
		version = max(version, a.Version)
	}
	return version, nil
}
