package storage

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// IsUniqueViolation reports whether err came from a unique or primary-key
// constraint.
//
// Detecting this properly matters for correctness, not just for tidy errors.
// The alternative — checking whether a row exists before inserting it — races:
// two concurrent registrations both see nothing, both insert, and one gets an
// error the code does not recognise. Letting the constraint decide and then
// classifying the failure is the only version that is right under concurrency.
//
// The two drivers report it completely differently, which is exactly the kind
// of dialect difference this package exists to absorb.
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}

	// PostgreSQL: SQLSTATE 23505, unique_violation.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}

	// SQLite: extended result codes for the UNIQUE and PRIMARY KEY variants of
	// SQLITE_CONSTRAINT.
	var liteErr *sqlite.Error
	if errors.As(err, &liteErr) {
		switch liteErr.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return true
		}
	}
	return false
}
