package devices

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/CauaMora1s/Headnet/internal/auth"
)

const deviceTokenPrefix = "hnd_"

// ErrDeviceTokenInvalid hides whether a credential is unknown or revoked.
var ErrDeviceTokenInvalid = errors.New("the device credential is not valid")

func validDeviceToken(token string) bool {
	if !strings.HasPrefix(token, deviceTokenPrefix) || len(token) != len(deviceTokenPrefix)+43 {
		return false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(token, deviceTokenPrefix))
	return err == nil && len(raw) == 32
}

// Authenticate returns only the device bound to this credential. There is no
// authentication cache: completed revocations take effect on the next lookup.
func (s *Store) Authenticate(ctx context.Context, token string) (*Device, error) {
	if !validDeviceToken(token) {
		return nil, ErrDeviceTokenInvalid
	}
	rows, err := s.db.QueryContext(ctx, s.db.Rebind(`SELECT `+deviceColumns+`
		FROM devices WHERE revoked_at IS NULL AND id IN
		(SELECT device_id FROM device_tokens WHERE token_hash = ?)`), auth.HashToken(token))
	if err != nil {
		return nil, fmt.Errorf("authenticating the device: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("authenticating the device: %w", err)
		}
		return nil, ErrDeviceTokenInvalid
	}
	return scanDevice(rows)
}

// Heartbeat checks the credential and revocation in the write itself. A request
// authenticated before a concurrent revocation cannot check in afterwards.
// The timestamp records control-plane contact, not a working VPN tunnel.
func (s *Store) Heartbeat(ctx context.Context, token string, now time.Time) error {
	if !validDeviceToken(token) {
		return ErrDeviceTokenInvalid
	}
	result, err := s.db.ExecContext(ctx, s.db.Rebind(`UPDATE devices SET last_seen_at = ?
		WHERE revoked_at IS NULL AND id IN
		(SELECT device_id FROM device_tokens WHERE token_hash = ?)`), now, auth.HashToken(token))
	if err != nil {
		return fmt.Errorf("recording the authenticated check-in: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking the authenticated check-in: %w", err)
	}
	if n != 1 {
		return ErrDeviceTokenInvalid
	}
	return nil
}
