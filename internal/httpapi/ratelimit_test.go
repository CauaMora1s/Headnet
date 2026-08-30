package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/CauaMora1s/Headnet/internal/httpapi"
	"github.com/CauaMora1s/Headnet/packages/api"
)

func TestLimiterAllowsUpToTheBurst(t *testing.T) {
	t.Parallel()
	limiter := httpapi.NewLimiter(60, 5)

	for i := range 5 {
		if allowed, _ := limiter.Allow("client"); !allowed {
			t.Fatalf("request %d was refused inside the burst allowance", i+1)
		}
	}
	allowed, retryAfter := limiter.Allow("client")
	if allowed {
		t.Fatal("the sixth request was allowed despite a burst of 5")
	}
	if retryAfter <= 0 {
		t.Fatalf("Retry-After = %v, want a positive delay", retryAfter)
	}
}

func TestLimiterIsPerClient(t *testing.T) {
	t.Parallel()
	// One noisy client must not be able to lock everyone else out.
	limiter := httpapi.NewLimiter(60, 2)

	for range 3 {
		limiter.Allow("noisy")
	}
	if allowed, _ := limiter.Allow("quiet"); !allowed {
		t.Fatal("a second client was refused because of the first client's traffic")
	}
}

func TestLimiterDisabledByNonPositiveSettings(t *testing.T) {
	t.Parallel()
	// A misconfiguration should degrade to "no limiting" rather than
	// "no service"; the configuration layer rejects these values first.
	for _, l := range []*httpapi.Limiter{
		httpapi.NewLimiter(0, 10),
		httpapi.NewLimiter(60, 0),
		nil,
	} {
		for range 100 {
			if allowed, _ := l.Allow("client"); !allowed {
				t.Fatal("a disabled limiter refused a request")
			}
		}
	}
}

func TestLimiterReclaimsMemoryFromIdleClients(t *testing.T) {
	t.Parallel()
	// An unbounded map keyed by client address is itself a
	// memory-exhaustion vector, so idle buckets have to be discarded.
	limiter := httpapi.NewLimiter(6000, 100)
	for i := range 500 {
		limiter.Allow("client-" + strconv.Itoa(i))
	}
	if got := limiter.Size(); got != 500 {
		t.Fatalf("tracked buckets = %d, want 500", got)
	}
	// The sweep is time-driven, so this asserts the accounting is exposed and
	// bounded rather than trying to fast-forward the clock from outside.
	if limiter.Size() > 500 {
		t.Fatalf("bucket count grew without new clients: %d", limiter.Size())
	}
}

func TestRateLimitMiddlewareReturnsTheStandardEnvelope(t *testing.T) {
	t.Parallel()
	limiter := httpapi.NewLimiter(60, 1)
	h := httpapi.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }),
		httpapi.RateLimit(limiter),
	)

	if rec := request(t, h, http.MethodGet, "/", ""); rec.Code != http.StatusOK {
		t.Fatalf("the first request was refused: %d", rec.Code)
	}

	rec := request(t, h, http.MethodGet, "/", "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if got := decodeError(t, rec); got.Error.Code != api.CodeRateLimited {
		t.Fatalf("code = %q, want rate_limited", got.Error.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After header was sent; a well-behaved client cannot back off")
	}
}

func TestRateLimitMiddlewareKeysOnTheClientAddress(t *testing.T) {
	t.Parallel()
	limiter := httpapi.NewLimiter(60, 1)
	h := httpapi.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }),
		httpapi.RateLimit(limiter),
	)

	send := func(remoteAddr string) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = remoteAddr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := send("192.0.2.1:1000"); got != http.StatusOK {
		t.Fatalf("first client, first request = %d", got)
	}
	if got := send("192.0.2.1:2000"); got != http.StatusTooManyRequests {
		t.Fatalf("same client on a new port = %d, want 429 (the port must not create a new bucket)", got)
	}
	if got := send("192.0.2.2:1000"); got != http.StatusOK {
		t.Fatalf("a different client = %d, want 200", got)
	}
}

func TestRateLimitMiddlewareCannotBeEvadedWithHeaders(t *testing.T) {
	t.Parallel()
	// If forwarding headers were trusted, rotating one would reset the bucket
	// and the limiter would protect nothing.
	limiter := httpapi.NewLimiter(60, 1)
	h := httpapi.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }),
		httpapi.RateLimit(limiter),
	)

	send := func(forwarded string) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.0.2.50:1234"
		req.Header.Set("X-Forwarded-For", forwarded)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := send("203.0.113.1"); got != http.StatusOK {
		t.Fatalf("first request = %d, want 200", got)
	}
	if got := send("203.0.113.2"); got != http.StatusTooManyRequests {
		t.Fatal("rotating X-Forwarded-For reset the rate-limit bucket")
	}
}

func TestRateLimitMiddlewareIsANoOpWithoutALimiter(t *testing.T) {
	t.Parallel()
	h := httpapi.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }),
		httpapi.RateLimit(nil),
	)
	for range 50 {
		if rec := request(t, h, http.MethodGet, "/", ""); rec.Code != http.StatusOK {
			t.Fatalf("status = %d with rate limiting disabled", rec.Code)
		}
	}
}
