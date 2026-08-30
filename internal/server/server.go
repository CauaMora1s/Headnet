// Package server assembles the Headnet control plane: the middleware chain,
// the route table, and the process lifecycle.
//
// What exists today is the foundation described in Phase 0 of
// docs/ROADMAP.md — health and readiness probes, build and protocol
// negotiation, and the operational scaffolding around them. Users, devices,
// authentication and peer distribution arrive in Phases 1 to 3. Endpoints for
// those are not stubbed out with plausible-looking empty responses; they
// simply do not exist yet, and asking for one returns 404.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/CauaMora1s/Headnet/internal/config"
	"github.com/CauaMora1s/Headnet/internal/httpapi"
	"github.com/CauaMora1s/Headnet/internal/storage"
	"github.com/CauaMora1s/Headnet/packages/shared"
)

// Options are the dependencies a Server needs. Everything is injected so the
// whole server can be built in a test without touching a real socket.
type Options struct {
	// Config is the validated server configuration.
	Config *config.Config
	// DB is an open, migrated database handle.
	DB *storage.DB
	// Logger receives structured records. Required.
	Logger *slog.Logger
	// InstanceID identifies this deployment; see storage.InstanceID.
	InstanceID string
	// Clock supplies the current time. Nil means the system clock.
	Clock shared.Clock
}

// Server is the control plane.
type Server struct {
	cfg        *config.Config
	db         *storage.DB
	logger     *slog.Logger
	clock      shared.Clock
	instanceID string
	startedAt  time.Time
	handler    http.Handler
}

// New builds a server and its route table.
func New(opts Options) (*Server, error) {
	switch {
	case opts.Config == nil:
		return nil, errors.New("server: a configuration is required")
	case opts.Logger == nil:
		return nil, errors.New("server: a logger is required")
	case opts.DB == nil:
		return nil, errors.New("server: a database handle is required")
	}
	// Re-validate rather than trusting the caller. A server that has been
	// handed an invalid configuration must not open a listener.
	if err := opts.Config.Validate(); err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}

	clock := opts.Clock
	if clock == nil {
		clock = shared.SystemClock
	}

	s := &Server{
		cfg:        opts.Config,
		db:         opts.DB,
		logger:     opts.Logger,
		clock:      clock,
		instanceID: opts.InstanceID,
		startedAt:  clock.Now(),
	}
	s.handler = s.buildHandler()
	return s, nil
}

// Handler returns the fully wrapped HTTP handler. Tests serve it directly with
// httptest rather than binding a port.
func (s *Server) Handler() http.Handler { return s.handler }

// InstanceID returns the identity of this deployment.
func (s *Server) InstanceID() string { return s.instanceID }

// buildHandler wraps the route table in the middleware chain.
//
// The order is deliberate and is the security-relevant part of this function:
//
//  1. Recoverer is outermost so it can catch a panic raised anywhere inside,
//     including in another middleware.
//  2. RequestID runs before the logger so every line, including one written
//     by the recoverer, carries an ID.
//  3. RateLimit runs before routing and before any authentication, so an
//     unauthenticated flood is rejected as cheaply as possible. A limiter that
//     only applies after authentication protects nothing that matters.
//  4. MaxBodyBytes runs before handlers so no handler can be made to buffer an
//     unbounded body.
func (s *Server) buildHandler() http.Handler {
	var limiter *httpapi.Limiter
	if s.cfg.RateLimit.Enabled {
		limiter = httpapi.NewLimiter(s.cfg.RateLimit.RequestsPerMinute, s.cfg.RateLimit.Burst)
	}

	// HSTS is only meaningful, and only safe, when clients genuinely reach
	// this deployment over HTTPS.
	hsts := false
	if u, err := url.Parse(s.cfg.Server.BaseURL); err == nil && u.Scheme == "https" {
		hsts = true
	}

	return httpapi.Chain(
		s.routes(),
		httpapi.Recoverer(s.logger),
		httpapi.RequestID(),
		httpapi.WithLogger(s.logger),
		httpapi.RequestLogger(s.logger),
		httpapi.SecurityHeaders(hsts),
		httpapi.RateLimit(limiter),
		httpapi.MaxBodyBytes(s.cfg.Server.MaxBodyBytes),
	)
}

// Run serves until the context is cancelled, then shuts down gracefully.
//
// Cancellation stops accepting new connections and gives in-flight requests up
// to ShutdownTimeout to finish. That matters for a control plane: a device
// enrollment interrupted halfway leaves a half-written record for an operator
// to clean up by hand.
func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.cfg.Server.Listen)
	if err != nil {
		return fmt.Errorf("binding %s: %w", s.cfg.Server.Listen, err)
	}
	return s.Serve(ctx, listener)
}

// Serve runs the server on an already-open listener. Tests use it to bind an
// ephemeral port without racing on a fixed one.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	httpServer := &http.Server{
		Handler:      s.handler,
		ReadTimeout:  s.cfg.Server.ReadTimeout,
		WriteTimeout: s.cfg.Server.WriteTimeout,
		IdleTimeout:  s.cfg.Server.IdleTimeout,
		// Header reads get their own, tighter bound: a slowloris client that
		// dribbles headers must not hold a connection for the full read
		// timeout.
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(s.logger.Handler(), slog.LevelWarn),
	}

	s.logger.InfoContext(ctx, "control plane listening",
		"address", listener.Addr().String(),
		"base_url", s.cfg.Server.BaseURL,
		"environment", string(s.cfg.Environment),
		"database", string(s.cfg.Database.Driver),
		"instance_id", s.instanceID,
	)

	serveErr := make(chan error, 1)
	go func() {
		err := httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	s.logger.InfoContext(ctx, "shutting down",
		"grace_period", s.cfg.Server.ShutdownTimeout.String())

	// Deliberately built from Background: the shutdown deadline must survive
	// the cancellation of the context that triggered it.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.Server.ShutdownTimeout)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		// Requests that had not finished within the grace period were cut
		// off. That is worth recording, not hiding.
		s.logger.WarnContext(ctx, "graceful shutdown did not complete in time", "error", err)
		return fmt.Errorf("shutting down: %w", err)
	}

	// Drain the goroutine so Serve does not outlive this call.
	if err := <-serveErr; err != nil {
		return err
	}
	s.logger.InfoContext(ctx, "shutdown complete")
	return nil
}

// uptime is how long this process has been serving.
func (s *Server) uptime() time.Duration {
	return s.clock.Now().Sub(s.startedAt)
}
