package httpapi

import (
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/headnet/headnet/internal/logging"
	"github.com/headnet/headnet/packages/api"
	"github.com/headnet/headnet/packages/shared"
)

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Chain composes middleware so that the first argument is the outermost layer.
// Reading the call site top to bottom then matches the order a request travels
// through them, which is the only ordering people reliably reason about
// correctly.
func Chain(h http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

// RequestIDHeader is where the per-request identifier is returned to clients.
const RequestIDHeader = "X-Request-Id"

// RequestID assigns every request a unique identifier, places it in the
// context for logging, and returns it to the client.
//
// An inbound X-Request-Id is deliberately ignored rather than adopted. It is
// attacker-controlled text that would otherwise be written verbatim into every
// log line for the request, which is a log-injection and log-forgery vector.
// Correlating with an upstream proxy's own ID is a Phase 12 concern and will
// require an explicit trusted-proxy configuration.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := shared.NewID("req")
			w.Header().Set(RequestIDHeader, id)
			next.ServeHTTP(w, r.WithContext(logging.WithRequestID(r.Context(), id)))
		})
	}
}

// WithLogger places the base logger in the request context so that handlers and
// the packages they call can log without being handed one explicitly.
func WithLogger(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(logging.WithLogger(r.Context(), logger)))
		})
	}
}

// Recoverer converts a panic into a 500 without taking the process down.
//
// One malformed request must not be able to kill a control plane that a whole
// network depends on. The stack trace goes to the log; the client is told
// nothing beyond the request ID, because a stack trace discloses internal
// paths and structure.
func Recoverer(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}
				// http.ErrAbortHandler is the documented way for a handler to
				// abandon a response on purpose. Swallowing it would turn a
				// deliberate abort into a spurious 500 in the logs.
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}
				logger.ErrorContext(r.Context(), "recovered from a panic while handling a request",
					"error", recovered,
					"method", r.Method,
					"path", r.URL.Path,
					"stack", string(debug.Stack()),
				)
				WriteError(w, r, api.CodeInternal, "internal error")
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// RequestLogger records one structured line per completed request.
//
// Only the path is logged, never the query string: OIDC callbacks and
// enrollment flows carry codes and tokens as query parameters, and logs are
// routinely shipped to places with weaker access controls than the database.
func RequestLogger(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			level := slog.LevelInfo
			switch {
			case rec.status >= http.StatusInternalServerError:
				level = slog.LevelError
			case rec.status >= http.StatusBadRequest:
				level = slog.LevelWarn
			}

			logger.Log(r.Context(), level, "handled request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"bytes", rec.written,
				"duration_ms", time.Since(start).Milliseconds(),
				"remote_ip", ClientIP(r),
			)
		})
	}
}

// SecurityHeaders sets response headers that harden the API and the web UI
// served alongside it.
//
// hsts should be enabled only when the deployment is genuinely reachable over
// HTTPS. Sending Strict-Transport-Security from a plain-HTTP development
// server would pin a developer's browser to HTTPS for localhost, which is
// tedious to undo and confusing to diagnose.
func SecurityHeaders(hsts bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			// Stop a browser from re-interpreting a JSON response as HTML or
			// script, which is the basis of several content-sniffing attacks.
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			// The API returns only JSON, so nothing it sends should ever be
			// allowed to load or execute a subresource.
			h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
			if hsts {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MaxBodyBytes caps request bodies. It is a denial-of-service control: without
// it, a single client can make the server buffer as much memory as it likes.
func MaxBodyBytes(limit int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimit rejects clients that exceed their request budget.
//
// A nil limiter disables the middleware entirely, which is what a
// configuration with rate limiting turned off produces.
func RateLimit(limiter *Limiter) Middleware {
	return func(next http.Handler) http.Handler {
		if limiter == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			allowed, retryAfter := limiter.Allow(ClientIP(r))
			if !allowed {
				// Telling the client exactly when to return is what lets a
				// well-behaved one back off instead of hammering.
				w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
				WriteErrorDetail(w, r, api.CodeRateLimited,
					"too many requests; slow down and try again shortly",
					"retry_after_seconds", strconv.Itoa(int(retryAfter.Seconds())))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP returns the address the request arrived from.
//
// It reads the transport-level peer address only. X-Forwarded-For and
// X-Real-IP are ignored because they are trivially forged by anyone who can
// reach the server directly, and honouring them without a trusted-proxy
// allowlist would let an attacker evade the rate limiter by rotating a header.
//
// The consequence is real and worth stating plainly: behind a reverse proxy,
// every request appears to come from the proxy, so rate limiting applies to
// the proxy as a whole rather than per client. Trusted-proxy support is a
// Phase 12 item; until then, apply per-client limits at the proxy.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr is not always host:port (a unix socket, or a test).
		return r.RemoteAddr
	}
	return host
}

// responseRecorder captures the status and size of a response for logging.
type responseRecorder struct {
	http.ResponseWriter
	status  int
	written int64
	// wroteHeader guards against a handler calling WriteHeader twice, which
	// would otherwise make the logged status disagree with the sent one.
	wroteHeader bool
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.wroteHeader = true
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		// Mirror net/http: the first Write implies a 200.
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(b)
	r.written += int64(n)
	return n, err
}

// Unwrap lets the standard library's ResponseController reach the underlying
// writer, so wrapping does not silently break flushing or hijacking.
func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
