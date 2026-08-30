package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/CauaMora1s/Headnet/packages/shared"
)

// Metadata keys. Each one is a deliberate, documented part of the deployment's
// identity, so they are declared here rather than spelled out at call sites.
const (
	// KeyInstanceID identifies this control-plane deployment. It is generated
	// on first start and never changes afterwards.
	KeyInstanceID = "instance_id"
	// KeyInitialisedAt records when the database was first migrated, which is
	// what tells an operator whether they are looking at a fresh install or a
	// restored backup.
	KeyInitialisedAt = "initialised_at"
)

// ErrMetadataNotFound means the key has never been set.
var ErrMetadataNotFound = errors.New("metadata key not found")

// GetMetadata reads one metadata value.
func GetMetadata(ctx context.Context, db *DB, key string) (string, error) {
	var value string
	query := db.Rebind(`SELECT value FROM server_metadata WHERE key = ?`)
	err := db.QueryRowContext(ctx, query, key).Scan(&value)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", fmt.Errorf("%w: %s", ErrMetadataNotFound, key)
	case err != nil:
		return "", fmt.Errorf("reading metadata key %s: %w", key, err)
	}
	return value, nil
}

// SetMetadata writes one metadata value, inserting or updating as needed.
func SetMetadata(ctx context.Context, db *DB, key, value string, now time.Time) error {
	query := db.Rebind(`
		INSERT INTO server_metadata (key, value, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`)
	if _, err := db.ExecContext(ctx, query, key, value, now, now); err != nil {
		return fmt.Errorf("writing metadata key %s: %w", key, err)
	}
	return nil
}

// InstanceID returns the identity of this control-plane deployment, generating
// and persisting it on first call.
//
// Clients record the instance ID they enrolled against. If a server is rebuilt
// from an empty database, the ID changes, and a client can tell it is now
// talking to a different network rather than silently trusting an impostor at
// the same address. That check is a Phase 2 client feature; persisting the ID
// from the start is what makes it possible later.
//
// The insert is conditional and the value is re-read afterwards, so two servers
// starting simultaneously against one database converge on a single ID rather
// than racing to overwrite each other.
func InstanceID(ctx context.Context, db *DB, now time.Time) (string, error) {
	if id, err := GetMetadata(ctx, db, KeyInstanceID); err == nil {
		return id, nil
	} else if !errors.Is(err, ErrMetadataNotFound) {
		return "", err
	}

	candidate := shared.NewID("net")

	err := db.InTx(ctx, func(tx *sql.Tx) error {
		// DO NOTHING rather than DO UPDATE: whoever inserted first wins, and
		// the loser reads their value back below.
		query := db.Rebind(`
			INSERT INTO server_metadata (key, value, created_at, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (key) DO NOTHING`)
		if _, err := tx.ExecContext(ctx, query, KeyInstanceID, candidate, now, now); err != nil {
			return err
		}
		query = db.Rebind(`
			INSERT INTO server_metadata (key, value, created_at, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (key) DO NOTHING`)
		_, err := tx.ExecContext(ctx, query, KeyInitialisedAt, now.UTC().Format(time.RFC3339), now, now)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("initialising the instance identity: %w", err)
	}

	id, err := GetMetadata(ctx, db, KeyInstanceID)
	if err != nil {
		return "", fmt.Errorf("initialising the instance identity: %w", err)
	}
	return id, nil
}

// KeyBootstrapClaimedAt records when the first administrator account was
// created. Its presence is what marks an installation as claimed.
const KeyBootstrapClaimedAt = "bootstrap_claimed_at"

// ClaimBootstrap attempts to mark the installation as claimed, reporting
// whether this caller was the one that did it.
//
// The insert is conditional, so exactly one caller can ever win, however many
// try at once. That matters: the first-run flow is open until it is claimed,
// and two simultaneous requests both creating an administrator would be a very
// bad way to discover the check was not atomic.
//
// It runs inside the caller's transaction so the claim and the account it
// authorises are committed together or not at all.
func ClaimBootstrap(ctx context.Context, db *DB, tx *sql.Tx, now time.Time) (bool, error) {
	result, err := tx.ExecContext(ctx, db.Rebind(`
		INSERT INTO server_metadata (key, value, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (key) DO NOTHING`),
		KeyBootstrapClaimedAt, now.UTC().Format(time.RFC3339), now, now)
	if err != nil {
		return false, fmt.Errorf("claiming the installation: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claiming the installation: %w", err)
	}
	return affected == 1, nil
}

// BootstrapClaimed reports whether the installation has an administrator.
func BootstrapClaimed(ctx context.Context, db *DB) (bool, error) {
	_, err := GetMetadata(ctx, db, KeyBootstrapClaimedAt)
	switch {
	case errors.Is(err, ErrMetadataNotFound):
		return false, nil
	case err != nil:
		return false, err
	}
	return true, nil
}
