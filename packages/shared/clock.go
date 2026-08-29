// SPDX-License-Identifier: Apache-2.0
// Copyright The Headnet Authors

package shared

import "time"

// Clock abstracts the passage of time so that expiry logic (setup keys,
// sessions, peer liveness) can be tested deterministically instead of with
// sleeps.
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts a plain function to the Clock interface.
type ClockFunc func() time.Time

// Now implements Clock.
func (f ClockFunc) Now() time.Time { return f() }

// SystemClock reads the real wall clock. Timestamps are normalised to UTC so
// that everything written to the database and to audit logs is comparable
// regardless of the server's local timezone.
var SystemClock Clock = ClockFunc(func() time.Time { return time.Now().UTC() })

// FixedClock returns a Clock that always reports t. It exists for tests.
func FixedClock(t time.Time) Clock {
	return ClockFunc(func() time.Time { return t })
}
