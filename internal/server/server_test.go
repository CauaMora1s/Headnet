package server_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CauaMora1s/Headnet/internal/config"
	"github.com/CauaMora1s/Headnet/internal/logging"
	"github.com/CauaMora1s/Headnet/internal/server"
	"github.com/CauaMora1s/Headnet/internal/storage"
	"github.com/CauaMora1s/Headnet/packages/api"
	"github.com/CauaMora1s/Headnet/packages/protocol"
)

// newServer builds a server backed by an isolated in-memory database.
func newServer(t *testing.T, tweak func(*config.Config)) *server.Server {
	t.Helper()

	cfg := config.Default()
	// Rate limiting is exercised in its own tests; leaving it on here would
	// make unrelated cases fail once they exceed the burst.
	cfg.RateLimit.Enabled = false
	if tweak != nil {
		tweak(cfg)
	}

	db := newDB(t)
	srv, err := server.New(server.Options{
		Config:     cfg,
		DB:         db,
		Logger:     logging.Discard(),
		InstanceID: "net_TEST",
	})
	if err != nil {
		t.Fatalf("server.New failed: %v", err)
	}
	return srv
}

func newDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(t.Context(), storage.Options{
		Driver: storage.DriverSQLite,
		Path:   storage.MemoryPath,
	})
	if err != nil {
		t.Fatalf("opening the database failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := storage.Migrate(t.Context(), db, nil); err != nil {
		t.Fatalf("migrating failed: %v", err)
	}
	return db
}

// do sends a request to the server and returns the recorder.
func do(t *testing.T, srv *server.Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.RemoteAddr = "192.0.2.1:1234"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding the response failed: %v (body %q)", err, rec.Body.String())
	}
	return out
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	t.Parallel()
	db := newDB(t)
	cfg := config.Default()

	tests := []struct {
		name string
		opts server.Options
	}{
		{"no config", server.Options{DB: db, Logger: logging.Discard()}},
		{"no logger", server.Options{Config: cfg, DB: db}},
		{"no database", server.Options{Config: cfg, Logger: logging.Discard()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := server.New(tt.opts); err == nil {
				t.Fatal("server.New accepted incomplete options")
			}
		})
	}
}

func TestNewRefusesAnInvalidConfiguration(t *testing.T) {
	t.Parallel()
	// A server handed a bad configuration must not open a listener, even if
	// the caller skipped validation.
	cfg := config.Default()
	cfg.Network.IPv4CIDR = "127.0.0.0/8"

	_, err := server.New(server.Options{
		Config: cfg,
		DB:     newDB(t),
		Logger: logging.Discard(),
	})
	if err == nil {
		t.Fatal("server.New accepted a configuration that hands out loopback addresses")
	}
}

func TestHealthReportsOKWhenTheDatabaseIsReachable(t *testing.T) {
	t.Parallel()
	rec := do(t, newServer(t, nil), http.MethodGet, "/health", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	got := decode[api.HealthResponse](t, rec)
	if got.Status != api.StatusOK {
		t.Errorf("status = %q, want ok", got.Status)
	}
	if got.Version == "" {
		t.Error("the health response carries no version")
	}
	if len(got.Checks) == 0 {
		t.Fatal("the health response lists no dependency checks")
	}
	if got.Checks[0].Name != "database" || got.Checks[0].Status != api.StatusOK {
		t.Errorf("database check = %+v, want an ok database check", got.Checks[0])
	}
}

func TestHealthGoesDownWhenTheDatabaseIsGone(t *testing.T) {
	// /ready has to take a broken instance out of a load balancer's rotation.
	cfg := config.Default()
	cfg.RateLimit.Enabled = false

	db := newDB(t)
	srv, err := server.New(server.Options{
		Config: cfg, DB: db, Logger: logging.Discard(), InstanceID: "net_TEST",
	})
	if err != nil {
		t.Fatalf("server.New failed: %v", err)
	}

	db.Close()

	for _, path := range []string{"/health", "/ready"} {
		rec := do(t, srv, http.MethodGet, path, "")
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s status = %d, want 503", path, rec.Code)
		}
		if got := decode[api.HealthResponse](t, rec); got.Status != api.StatusDown {
			t.Errorf("%s status = %q, want down", path, got.Status)
		}
	}
}

func TestHealthDoesNotLeakInternals(t *testing.T) {
	// /health is unauthenticated, so anything it says is public.
	cfg := config.Default()
	cfg.RateLimit.Enabled = false
	cfg.Database.Path = "/very/secret/path/headnet.db"

	db := newDB(t)
	srv, err := server.New(server.Options{
		Config: cfg, DB: db, Logger: logging.Discard(), InstanceID: "net_SECRET_INSTANCE",
	})
	if err != nil {
		t.Fatalf("server.New failed: %v", err)
	}
	db.Close()

	body := do(t, srv, http.MethodGet, "/health", "").Body.String()
	for _, leak := range []string{"/very/secret/path", "net_SECRET_INSTANCE", "sqlite3", "modernc"} {
		if strings.Contains(body, leak) {
			t.Errorf("the health response leaked %q:\n%s", leak, body)
		}
	}
}

func TestLiveIgnoresDependencies(t *testing.T) {
	// Liveness answers "should this container be restarted?". Restarting the
	// control plane over a brief database outage turns a recoverable problem
	// into a crash loop.
	cfg := config.Default()
	cfg.RateLimit.Enabled = false

	db := newDB(t)
	srv, err := server.New(server.Options{
		Config: cfg, DB: db, Logger: logging.Discard(),
	})
	if err != nil {
		t.Fatalf("server.New failed: %v", err)
	}
	db.Close()

	rec := do(t, srv, http.MethodGet, "/live", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("/live status = %d, want 200 even with the database down", rec.Code)
	}
}

func TestReadyReportsDegradedWhenUnmigrated(t *testing.T) {
	cfg := config.Default()
	cfg.RateLimit.Enabled = false

	// An open but unmigrated database.
	db, err := storage.Open(t.Context(), storage.Options{
		Driver: storage.DriverSQLite, Path: storage.MemoryPath,
	})
	if err != nil {
		t.Fatalf("opening the database failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	srv, err := server.New(server.Options{Config: cfg, DB: db, Logger: logging.Discard()})
	if err != nil {
		t.Fatalf("server.New failed: %v", err)
	}

	got := decode[api.HealthResponse](t, do(t, srv, http.MethodGet, "/ready", ""))
	if got.Status == api.StatusOK {
		t.Fatal("/ready reported ok against an unmigrated database")
	}
}

func TestVersionEndpoint(t *testing.T) {
	t.Parallel()
	rec := do(t, newServer(t, nil), http.MethodGet, "/api/v1/version", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := decode[api.VersionResponse](t, rec)
	if got.ProtocolVersion != protocol.Version {
		t.Errorf("protocol_version = %d, want %d", got.ProtocolVersion, protocol.Version)
	}
	if got.MinProtocolVersion != protocol.MinVersion {
		t.Errorf("min_protocol_version = %d, want %d", got.MinProtocolVersion, protocol.MinVersion)
	}
	if len(got.Capabilities) == 0 {
		t.Error("the server advertises no capabilities")
	}
	if got.Version == "" || got.Commit == "" {
		t.Errorf("build identity is incomplete: %+v", got)
	}
}

func TestNegotiateAgreesWithAMatchingClient(t *testing.T) {
	t.Parallel()
	body := `{"protocol_version":` + strconv.Itoa(protocol.Version) +
		`,"capabilities":["protocol.negotiation"],"client_version":"0.1.0"}`

	rec := do(t, newServer(t, nil), http.MethodPost, "/api/v1/protocol/negotiate", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}

	got := decode[api.NegotiateResponse](t, rec)
	if got.ProtocolVersion != protocol.Version {
		t.Errorf("protocol_version = %d, want %d", got.ProtocolVersion, protocol.Version)
	}
	if len(got.Capabilities) != 1 || got.Capabilities[0] != string(protocol.CapabilityProtocolNegotiation) {
		t.Errorf("capabilities = %v, want the intersection", got.Capabilities)
	}
}

func TestNegotiateIgnoresUnknownCapabilities(t *testing.T) {
	t.Parallel()
	// A newer client may advertise things this server has never heard of;
	// that must degrade gracefully rather than fail.
	body := `{"protocol_version":` + strconv.Itoa(protocol.Version) +
		`,"capabilities":["protocol.negotiation","quantum.teleport"]}`

	rec := do(t, newServer(t, nil), http.MethodPost, "/api/v1/protocol/negotiate", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	for _, c := range decode[api.NegotiateResponse](t, rec).Capabilities {
		if c == "quantum.teleport" {
			t.Fatal("the server agreed to a capability it does not implement")
		}
	}
}

func TestNegotiateRejectsAnIncompatibleClient(t *testing.T) {
	t.Parallel()
	// A client from a future major protocol version that cannot speak ours.
	body := `{"protocol_version":9999,"min_protocol_version":9999}`

	rec := do(t, newServer(t, nil), http.MethodPost, "/api/v1/protocol/negotiate", body)
	if rec.Code != http.StatusUpgradeRequired {
		t.Fatalf("status = %d, want 426", rec.Code)
	}
	got := decode[api.ErrorResponse](t, rec)
	if got.Error.Code != api.CodeUnsupportedProtocol {
		t.Errorf("code = %q, want unsupported_protocol_version", got.Error.Code)
	}
	if got.Error.Details["server_protocol_version"] == "" {
		t.Error("the error does not tell the client what the server speaks")
	}
}

func TestNegotiateValidatesItsInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{"missing version", `{}`},
		{"zero version", `{"protocol_version":0}`},
		{"negative version", `{"protocol_version":-1}`},
		{"min above max", `{"protocol_version":1,"min_protocol_version":5}`},
		{"unknown field", `{"protocol_version":1,"protocol_vresion":1}`},
		{"not json", `nonsense`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := do(t, newServer(t, nil), http.MethodPost, "/api/v1/protocol/negotiate", tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %q)", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMetricsAreAbsentUnlessEnabled(t *testing.T) {
	t.Parallel()
	rec := do(t, newServer(t, nil), http.MethodGet, "/metrics", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when metrics are disabled", rec.Code)
	}
}

func TestMetricsAreHonestlyUnimplementedWhenEnabled(t *testing.T) {
	t.Parallel()
	// An empty but valid metrics page would make a dashboard show a healthy,
	// entirely fictional zero.
	srv := newServer(t, func(c *config.Config) { c.Observability.MetricsEnabled = true })

	rec := do(t, srv, http.MethodGet, "/metrics", "")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
	if got := decode[api.ErrorResponse](t, rec); got.Error.Details["roadmap_phase"] == "" {
		t.Error("the response does not say which phase will deliver metrics")
	}
}

func TestUnimplementedFeaturesAreAbsentRatherThanFaked(t *testing.T) {
	t.Parallel()
	// Devices, users, routes and policies arrive in later phases. Until then
	// they must not answer at all — an empty list would read as "you have no
	// devices" rather than "this does not exist yet".
	srv := newServer(t, nil)
	for _, path := range []string{
		"/api/v1/devices", "/api/v1/users", "/api/v1/routes", "/api/v1/policies", "/api/v1/peers",
	} {
		rec := do(t, srv, http.MethodGet, path, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", path, rec.Code)
		}
		if got := decode[api.ErrorResponse](t, rec); got.Error.Code != api.CodeNotFound {
			t.Errorf("%s code = %q, want not_found", path, got.Error.Code)
		}
	}
}

func TestUnknownRoutesUseTheStandardEnvelope(t *testing.T) {
	t.Parallel()
	rec := do(t, newServer(t, nil), http.MethodGet, "/no/such/thing", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want JSON", ct)
	}
}

func TestWrongMethodIsRejected(t *testing.T) {
	t.Parallel()
	rec := do(t, newServer(t, nil), http.MethodPost, "/health", "")
	if rec.Code == http.StatusOK {
		t.Fatal("POST /health succeeded; the method pattern is not being enforced")
	}
}

func TestEveryResponseCarriesSecurityHeadersAndARequestID(t *testing.T) {
	t.Parallel()
	srv := newServer(t, nil)
	for _, path := range []string{"/health", "/api/v1/version", "/no/such/thing"} {
		rec := do(t, srv, http.MethodGet, path, "")
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", path, got)
		}
		if rec.Header().Get("X-Request-Id") == "" {
			t.Errorf("%s: no request ID was returned", path)
		}
	}
}

func TestHSTSFollowsTheBaseURLScheme(t *testing.T) {
	t.Parallel()
	plain := newServer(t, nil)
	if got := do(t, plain, http.MethodGet, "/health", "").Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS was sent over a plain-HTTP deployment: %q", got)
	}

	secure := newServer(t, func(c *config.Config) {
		c.Server.BaseURL = "https://vpn.example.com"
	})
	if got := do(t, secure, http.MethodGet, "/health", "").Header().Get("Strict-Transport-Security"); got == "" {
		t.Error("HSTS was not sent for an HTTPS deployment")
	}
}

func TestRateLimitingIsAppliedBeforeRouting(t *testing.T) {
	// An unauthenticated flood must be rejected as cheaply as possible, which
	// means before the router and before any handler runs.
	srv := newServer(t, func(c *config.Config) {
		c.RateLimit.Enabled = true
		c.RateLimit.RequestsPerMinute = 60
		c.RateLimit.Burst = 3
	})

	var lastCode int
	for range 10 {
		lastCode = do(t, srv, http.MethodGet, "/no/such/route", "").Code
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("final status = %d, want 429 after exceeding the burst", lastCode)
	}
}

func TestRequestBodiesAreCapped(t *testing.T) {
	t.Parallel()
	srv := newServer(t, func(c *config.Config) { c.Server.MaxBodyBytes = 64 })

	body := `{"protocol_version":1,"client_version":"` + strings.Repeat("x", 4096) + `"}`
	rec := do(t, srv, http.MethodPost, "/api/v1/protocol/negotiate", body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestServeShutsDownGracefully(t *testing.T) {
	srv := newServer(t, func(c *config.Config) {
		c.Server.ShutdownTimeout = 2 * time.Second
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding an ephemeral port failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, listener) }()

	// The server must actually be serving before shutdown is meaningful.
	url := "http://" + listener.Addr().String() + "/live"
	waitForServer(t, url)

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned an error on graceful shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after its context was cancelled")
	}

	// The listener must be closed, so a follow-up request cannot succeed.
	if _, err := http.Get(url); err == nil {
		t.Fatal("the server is still accepting requests after shutdown")
	}
}

func TestRunReportsABindFailure(t *testing.T) {
	// Two servers on one port: the second must fail loudly at start-up rather
	// than appear to run.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding failed: %v", err)
	}
	defer occupied.Close()

	srv := newServer(t, func(c *config.Config) {
		c.Server.Listen = occupied.Addr().String()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Run(ctx); err == nil {
		t.Fatal("Run succeeded despite the port already being in use")
	}
}

func TestInstanceIDIsExposedToTheProcessButNotToClients(t *testing.T) {
	t.Parallel()
	srv := newServer(t, nil)
	if srv.InstanceID() != "net_TEST" {
		t.Fatalf("InstanceID() = %q, want net_TEST", srv.InstanceID())
	}
	// It identifies the deployment and has no business on an unauthenticated
	// endpoint.
	if body := do(t, srv, http.MethodGet, "/health", "").Body.String(); strings.Contains(body, "net_TEST") {
		t.Fatalf("/health disclosed the instance ID:\n%s", body)
	}
}

// waitForServer blocks until the address answers or the attempt budget runs out.
func waitForServer(t *testing.T, url string) {
	t.Helper()
	for range 100 {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the server never became reachable at %s", url)
}
