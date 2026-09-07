package server

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/CauaMora1s/Headnet/internal/httpapi"
	"github.com/CauaMora1s/Headnet/internal/storage"
	"github.com/CauaMora1s/Headnet/internal/version"
	"github.com/CauaMora1s/Headnet/packages/api"
	"github.com/CauaMora1s/Headnet/packages/protocol"
)

// routes builds the route table.
//
// Go's ServeMux has supported method-and-pattern routing since 1.22, which is
// everything the control plane needs. Adding a third-party router would buy
// nothing and would put a dependency on the path every request takes.
// See docs/architecture/decisions/ADR-0010-standard-library-http-router.md.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Operational probes. These are unauthenticated by design so that load
	// balancers and orchestrators can reach them, which is exactly why they
	// disclose nothing beyond a release string and dependency status.
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /live", s.handleLive)
	mux.HandleFunc("GET /ready", s.handleReady)
	mux.HandleFunc("GET /metrics", s.handleMetrics)

	// Versioned API.
	mux.HandleFunc("GET "+api.BasePath+"/version", s.handleVersion)
	mux.HandleFunc("POST "+api.BasePath+"/protocol/negotiate", s.handleNegotiate)

	// Authentication. These three are unauthenticated by necessity, which is
	// exactly why they carry the tighter rate limit: repeated guessing is the
	// whole attack against them.
	mux.Handle("GET "+api.BasePath+"/auth/status",
		s.rateLimitAuth(http.HandlerFunc(s.handleAuthStatus)))
	mux.Handle("POST "+api.BasePath+"/auth/login",
		s.rateLimitAuth(http.HandlerFunc(s.handleLogin)))
	mux.Handle("POST "+api.BasePath+"/auth/bootstrap",
		s.rateLimitAuth(http.HandlerFunc(s.handleBootstrap)))

	// Logout needs a session and a CSRF token: signing someone out from
	// another site is a small attack, but it is still one.
	mux.Handle("POST "+api.BasePath+"/auth/logout",
		s.authenticated(http.HandlerFunc(s.handleLogout), false))
	mux.Handle("GET "+api.BasePath+"/auth/session",
		s.authenticated(http.HandlerFunc(s.handleSession), false))

	// Devices. Everything here needs a session except enrolment, where the
	// setup key is itself the credential — so that one carries the tighter
	// authentication rate limit instead.
	mux.Handle("GET "+api.BasePath+"/devices",
		s.authenticated(http.HandlerFunc(s.handleListDevices), false))
	mux.Handle("POST "+api.BasePath+"/devices",
		s.authenticated(http.HandlerFunc(s.handleRegisterDevice), false))
	mux.Handle("GET "+api.BasePath+"/devices/{id}",
		s.authenticated(http.HandlerFunc(s.handleGetDevice), false))
	mux.Handle("DELETE "+api.BasePath+"/devices/{id}",
		s.authenticated(http.HandlerFunc(s.handleRevokeDevice), false))
	mux.Handle("POST "+api.BasePath+"/devices/enroll",
		s.rateLimitAuth(http.HandlerFunc(s.handleEnrollDevice)))
	// Device credentials are deliberately separate from browser sessions.
	// These routes use the global request budget, not rateLimitAuth: routine
	// heartbeats must not compete with sign-in attempts for the tighter budget.
	mux.Handle("GET "+api.BasePath+"/devices/me",
		s.requireDevice(s.handleDeviceMe))
	mux.Handle("POST "+api.BasePath+"/devices/me/heartbeat",
		s.requireDevice(s.handleDeviceHeartbeat))
	mux.Handle("GET "+api.BasePath+"/network/config",
		s.requireDevice(s.handleDeviceNetworkConfig))

	// Setup keys are administrative: they mint credentials that enrol devices
	// onto the network, so issuing one is an administrator's decision.
	mux.Handle("POST "+api.BasePath+"/setup-keys",
		s.authenticated(http.HandlerFunc(s.handleCreateSetupKey), true))
	mux.Handle("GET "+api.BasePath+"/setup-keys",
		s.authenticated(http.HandlerFunc(s.handleListSetupKeys), true))
	mux.Handle("DELETE "+api.BasePath+"/setup-keys/{id}",
		s.authenticated(http.HandlerFunc(s.handleRevokeSetupKey), true))

	// Anything unmatched gets the standard envelope rather than Go's
	// plain-text default, so a client only ever has to parse one error shape.
	mux.Handle("/", httpapi.NotFoundHandler())

	return mux
}

// healthCheckTimeout bounds a dependency probe. A probe that hangs is a
// failing probe: an orchestrator waiting on /ready needs an answer, not a
// stalled connection.
const healthCheckTimeout = 3 * time.Second

// handleLive reports that the process is running and able to serve.
//
// It deliberately checks nothing else. Liveness is "should this container be
// restarted?", and restarting the control plane because the database is
// briefly unreachable would turn a recoverable outage into a crash loop.
func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	httpapi.WriteJSON(w, r, http.StatusOK, api.HealthResponse{
		Status:        api.StatusOK,
		Version:       version.Get().Version,
		UptimeSeconds: int64(s.uptime().Seconds()),
		Checks:        []api.Check{},
	})
}

// handleReady reports whether the server can serve real traffic, which means
// the database is reachable and migrated to the schema this build expects.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	resp := s.probe(r.Context())

	status := http.StatusOK
	if resp.Status == api.StatusDown {
		// A load balancer must take this instance out of rotation.
		status = http.StatusServiceUnavailable
	}
	httpapi.WriteJSON(w, r, status, resp)
}

// handleHealth reports the aggregate status with per-dependency detail.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := s.probe(r.Context())

	status := http.StatusOK
	if resp.Status == api.StatusDown {
		status = http.StatusServiceUnavailable
	}
	httpapi.WriteJSON(w, r, status, resp)
}

// probe runs every dependency check and folds the results into one status.
func (s *Server) probe(ctx context.Context) api.HealthResponse {
	checks := []api.Check{s.checkDatabase(ctx)}

	overall := api.StatusOK
	for _, c := range checks {
		overall = overall.Worse(c.Status)
	}

	return api.HealthResponse{
		Status:        overall,
		Version:       version.Get().Version,
		UptimeSeconds: int64(s.uptime().Seconds()),
		Checks:        checks,
	}
}

// checkDatabase verifies that the database answers and is migrated.
//
// The detail field is written for an operator and never carries a DSN, a
// hostname or a driver error verbatim: /health is unauthenticated, so anything
// it says is public.
func (s *Server) checkDatabase(ctx context.Context) api.Check {
	ctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()

	start := s.clock.Now()
	check := api.Check{Name: "database", Status: api.StatusOK}
	finish := func() api.Check {
		check.DurationMS = s.clock.Now().Sub(start).Milliseconds()
		return check
	}

	if err := s.db.PingContext(ctx); err != nil {
		s.logger.ErrorContext(ctx, "database health check failed", "error", err)
		check.Status = api.StatusDown
		check.Detail = "the database is not reachable"
		return finish()
	}

	applied, err := storage.SchemaVersion(ctx, s.db)
	if err != nil {
		s.logger.ErrorContext(ctx, "reading the schema version failed", "error", err)
		check.Status = api.StatusDown
		check.Detail = "the schema version could not be read"
		return finish()
	}

	expected, err := storage.LoadMigrations(s.db.Driver())
	if err != nil || len(expected) == 0 {
		check.Status = api.StatusDown
		check.Detail = "the embedded migrations could not be read"
		return finish()
	}
	if want := expected[len(expected)-1].Version; applied != want {
		// Degraded, not down: the server is answering, but an operator has an
		// upgrade step left to run.
		check.Status = api.StatusDegraded
		check.Detail = "the database schema is not at the version this build expects; run the server once to migrate"
		return finish()
	}

	return finish()
}

// handleMetrics is a declared endpoint with no implementation behind it yet.
//
// When metrics are disabled the endpoint does not exist at all. When they are
// enabled it returns 501, because Prometheus instrumentation is a Phase 12
// item: returning an empty but valid metrics page would make a monitoring
// dashboard show a healthy, entirely fictional zero.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Observability.MetricsEnabled {
		httpapi.WriteError(w, r, api.CodeNotFound, "the metrics endpoint is disabled")
		return
	}
	httpapi.NotImplemented(w, r, "Prometheus metrics", "Phase 12")
}

// handleVersion reports the build and the protocol range this server speaks.
// It is the first call a client makes, and the basis of the compatibility
// check described in docs/architecture/protocol-versioning.md.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	build := version.Get()
	httpapi.WriteJSON(w, r, http.StatusOK, api.VersionResponse{
		Version:            build.Version,
		Commit:             build.Commit,
		BuildDate:          build.BuildDate,
		ProtocolVersion:    protocol.Version,
		MinProtocolVersion: protocol.MinVersion,
		Capabilities:       protocol.NewSet(protocol.Supported()...).Strings(),
	})
}

// handleNegotiate agrees a protocol version and capability set with a client.
//
// A client and a server are upgraded independently, so this endpoint exists to
// turn a version mismatch into one clear, actionable error at the start of a
// session rather than a confusing failure several requests later.
func (s *Server) handleNegotiate(w http.ResponseWriter, r *http.Request) {
	var req api.NegotiateRequest
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		return
	}

	if req.ProtocolVersion <= 0 {
		httpapi.WriteErrorDetail(w, r, api.CodeBadRequest,
			"protocol_version must be a positive integer", "field", "protocol_version")
		return
	}
	// A client that does not say how far back it can go is assumed to speak
	// exactly one version.
	minVersion := req.MinProtocolVersion
	if minVersion <= 0 {
		minVersion = req.ProtocolVersion
	}
	if minVersion > req.ProtocolVersion {
		httpapi.WriteErrorDetail(w, r, api.CodeBadRequest,
			"min_protocol_version must not exceed protocol_version", "field", "min_protocol_version")
		return
	}

	negotiatedVersion, negotiatedCaps, err := protocol.Negotiate(
		protocol.Local(),
		protocol.Range{Version: req.ProtocolVersion, Min: minVersion},
		protocol.NewSet(protocol.Supported()...),
		protocol.ParseCapabilities(req.Capabilities),
	)
	if err != nil {
		s.logger.WarnContext(r.Context(), "rejected an incompatible client",
			"client_version", req.ClientVersion,
			"client_protocol_version", req.ProtocolVersion,
			"client_min_protocol_version", minVersion,
		)
		httpapi.WriteErrorDetail(w, r, api.CodeUnsupportedProtocol, err.Error(),
			"server_protocol_version", strconv.Itoa(protocol.Version))
		return
	}

	httpapi.WriteJSON(w, r, http.StatusOK, api.NegotiateResponse{
		ProtocolVersion: negotiatedVersion,
		Capabilities:    negotiatedCaps.Strings(),
		ServerVersion:   version.Get().Version,
	})
}
