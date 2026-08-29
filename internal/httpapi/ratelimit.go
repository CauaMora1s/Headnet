package httpapi

import (
	"math"
	"sync"
	"time"
)

// Limiter is a per-client token bucket.
//
// A control plane is reachable by anyone who can route to it, and several of
// its endpoints are necessarily unauthenticated: login, enrollment, protocol
// negotiation. Without a ceiling, those are free brute-force and
// resource-exhaustion targets. The limiter is therefore on by default and is
// applied before authentication, not after — a limiter that only runs for
// authenticated callers protects nothing that matters.
//
// Buckets are swept periodically. An unbounded map keyed by client address is
// itself a memory-exhaustion vector: an attacker with a large address range
// could otherwise force the server to allocate a bucket per source.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket

	// ratePerSecond is the sustained refill rate.
	ratePerSecond float64
	// burst is the bucket capacity, i.e. how far above the sustained rate a
	// well-behaved client may briefly spike.
	burst float64

	// now is injectable so the behaviour can be tested without sleeping.
	now func() time.Time

	lastSweep time.Time
	// sweepEvery bounds how often the map is scanned, so a busy server does
	// not pay for a full sweep on every request.
	sweepEvery time.Duration
	// idleTTL is how long a bucket may go untouched before it is discarded. A
	// full bucket carries no state worth keeping.
	idleTTL time.Duration
}

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

// Default sweep parameters. They trade a little memory for far fewer scans.
const (
	defaultSweepEvery = time.Minute
	defaultIdleTTL    = 10 * time.Minute
)

// NewLimiter builds a limiter allowing requestsPerMinute sustained requests per
// client with the given burst allowance.
//
// A non-positive rate or burst yields a limiter that allows everything, so a
// misconfiguration degrades to "no limiting" rather than "no service". The
// configuration layer rejects those values before they ever reach here.
func NewLimiter(requestsPerMinute, burst int) *Limiter {
	return newLimiter(requestsPerMinute, burst, time.Now)
}

func newLimiter(requestsPerMinute, burst int, now func() time.Time) *Limiter {
	return &Limiter{
		buckets:       make(map[string]*bucket),
		ratePerSecond: float64(requestsPerMinute) / 60.0,
		burst:         float64(burst),
		now:           now,
		lastSweep:     now(),
		sweepEvery:    defaultSweepEvery,
		idleTTL:       defaultIdleTTL,
	}
}

// Allow reports whether the client identified by key may make a request now.
//
// When it refuses, it also returns how long the caller should wait before the
// next token is available, which becomes the Retry-After header. Telling a
// client exactly when to come back is what turns a rate limit from an opaque
// failure into something a well-behaved client can cooperate with.
func (l *Limiter) Allow(key string) (allowed bool, retryAfter time.Duration) {
	if l == nil || l.ratePerSecond <= 0 || l.burst <= 0 {
		return true, 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweepLocked(now)

	b, exists := l.buckets[key]
	if !exists {
		// A new client starts with a full bucket and immediately spends one
		// token on the request being decided here.
		l.buckets[key] = &bucket{tokens: l.burst - 1, lastSeen: now}
		return true, 0
	}

	// Refill for the time that has passed, capped at the burst size.
	elapsed := now.Sub(b.lastSeen).Seconds()
	if elapsed > 0 {
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.ratePerSecond)
	}
	b.lastSeen = now

	if b.tokens < 1 {
		// Time until the bucket holds one whole token.
		deficit := 1 - b.tokens
		wait := time.Duration(deficit / l.ratePerSecond * float64(time.Second))
		return false, max(wait, time.Second)
	}

	b.tokens--
	return true, 0
}

// sweepLocked discards buckets that have been idle long enough to be
// indistinguishable from a client that never existed. The caller holds l.mu.
func (l *Limiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastSweep) < l.sweepEvery {
		return
	}
	l.lastSweep = now
	for key, b := range l.buckets {
		if now.Sub(b.lastSeen) > l.idleTTL {
			delete(l.buckets, key)
		}
	}
}

// Size reports how many buckets are being tracked. It exists so tests can
// assert that memory is actually reclaimed.
func (l *Limiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
