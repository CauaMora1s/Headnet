package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CauaMora1s/Headnet/internal/auth"
	"github.com/CauaMora1s/Headnet/internal/config"
	"github.com/CauaMora1s/Headnet/internal/logging"
	"github.com/CauaMora1s/Headnet/internal/server"
	"github.com/CauaMora1s/Headnet/internal/storage"
	"github.com/CauaMora1s/Headnet/internal/storage/storagetest"
	"github.com/CauaMora1s/Headnet/packages/api"
)

// newFreshServer builds a server over an empty database — nothing claimed, no
// accounts.
func newFreshServer(t *testing.T, tweak func(*config.Config)) (*server.Server, *storage.DB) {
	t.Helper()

	cfg := config.Default()
	cfg.RateLimit.Enabled = false
	if tweak != nil {
		tweak(cfg)
	}

	db := storagetest.Open(t)
	srv, err := server.New(server.Options{
		Config: cfg, DB: db, Logger: logging.Discard(), InstanceID: "net_TEST",
	})
	if err != nil {
		t.Fatalf("server.New failed: %v", err)
	}
	return srv, db
}

// post sends a JSON request to a server.
func post(t *testing.T, srv *server.Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.1:1234"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

const bootstrapBody = `{"email":"admin@example.com","password":"correct horse battery staple"}`

func TestBootstrapCreatesTheFirstAdministrator(t *testing.T) {
	srv, _ := newFreshServer(t, nil)

	rec := post(t, srv, "/api/v1/auth/bootstrap", bootstrapBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var session api.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if session.User.Role != string(auth.RoleAdmin) {
		t.Fatalf("role = %q, want admin; the first account has nobody to grant it later", session.User.Role)
	}
	if session.User.Email != "admin@example.com" {
		t.Errorf("email = %q", session.User.Email)
	}
	// It signs the operator straight in, so the flow is deploy, open, create,
	// and you are inside.
	if session.CSRFToken == "" {
		t.Error("bootstrap did not return a CSRF token")
	}
	if cookieNamed(rec.Result().Cookies(), auth.SessionCookieName) == nil {
		t.Error("bootstrap did not set a session cookie")
	}
}

func TestBootstrapCanOnlyHappenOnce(t *testing.T) {
	srv, _ := newFreshServer(t, nil)

	if rec := post(t, srv, "/api/v1/auth/bootstrap", bootstrapBody); rec.Code != http.StatusCreated {
		t.Fatalf("the first bootstrap failed: %d %s", rec.Code, rec.Body.String())
	}

	second := post(t, srv, "/api/v1/auth/bootstrap",
		`{"email":"attacker@example.com","password":"another long passphrase"}`)
	if second.Code != http.StatusConflict {
		t.Fatalf("the second bootstrap gave %d, want 409", second.Code)
	}

	var resp api.ErrorResponse
	if err := json.Unmarshal(second.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if resp.Error.Code != api.CodeConflict {
		t.Fatalf("code = %q, want conflict", resp.Error.Code)
	}
}

func TestConcurrentBootstrapYieldsExactlyOneAdministrator(t *testing.T) {
	// The endpoint is open until it is claimed, so simultaneous requests are a
	// realistic race rather than a theoretical one. Two administrators being
	// created would be a very bad way to discover the claim was not atomic.
	srv, db := newFreshServer(t, nil)

	const attempts = 12
	var wg sync.WaitGroup
	codes := make([]int, attempts)

	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := `{"email":"admin` + string(rune('a'+i)) + `@example.com","password":"correct horse battery staple"}`
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/bootstrap", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = "192.0.2.1:1234"
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			codes[i] = rec.Code
		}()
	}
	wg.Wait()

	created := 0
	for i, code := range codes {
		switch code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			// Expected for the losers.
		default:
			t.Errorf("attempt %d gave an unexpected %d", i, code)
		}
	}
	if created != 1 {
		t.Fatalf("%d of %d concurrent bootstraps succeeded, want exactly 1", created, attempts)
	}

	users := auth.NewUserStore(db, auth.StoreOptions{})
	count, err := users.Count(context.Background())
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("the database holds %d accounts, want 1", count)
	}
}

func TestAuthStatusReportsWhetherBootstrapIsNeeded(t *testing.T) {
	srv, _ := newFreshServer(t, nil)

	statusOf := func() api.AuthStatus {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/status", nil)
		req.RemoteAddr = "192.0.2.1:1234"
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var got api.AuthStatus
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decoding failed: %v", err)
		}
		return got
	}

	if !statusOf().BootstrapRequired {
		t.Fatal("bootstrap_required = false on a fresh installation")
	}
	if rec := post(t, srv, "/api/v1/auth/bootstrap", bootstrapBody); rec.Code != http.StatusCreated {
		t.Fatalf("bootstrap failed: %d", rec.Code)
	}
	if statusOf().BootstrapRequired {
		t.Fatal("bootstrap_required = true after the installation was claimed")
	}
}

func TestBootstrapEnforcesThePasswordPolicy(t *testing.T) {
	srv, _ := newFreshServer(t, nil)

	tests := []struct {
		name string
		body string
	}{
		{"short password", `{"email":"admin@example.com","password":"short"}`},
		{"no password", `{"email":"admin@example.com"}`},
		{"invalid email", `{"email":"notanemail","password":"correct horse battery staple"}`},
		{"no email", `{"password":"correct horse battery staple"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := post(t, srv, "/api/v1/auth/bootstrap", tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAFailedBootstrapLeavesTheInstallationClaimable(t *testing.T) {
	// The claim and the account are inserted in one transaction, so a rejected
	// account must not burn the claim and leave the server permanently
	// unusable.
	srv, db := newFreshServer(t, nil)

	if rec := post(t, srv, "/api/v1/auth/bootstrap",
		`{"email":"admin@example.com","password":"short"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("the invalid bootstrap gave %d, want 400", rec.Code)
	}

	claimed, err := storage.BootstrapClaimed(context.Background(), db)
	if err != nil {
		t.Fatalf("BootstrapClaimed failed: %v", err)
	}
	if claimed {
		t.Fatal("a rejected bootstrap claimed the installation, locking it permanently")
	}

	if rec := post(t, srv, "/api/v1/auth/bootstrap", bootstrapBody); rec.Code != http.StatusCreated {
		t.Fatalf("the retry after a rejected bootstrap gave %d, want 201: %s", rec.Code, rec.Body.String())
	}
}

func TestTheBootstrapAdministratorCanThenSignIn(t *testing.T) {
	srv, _ := newFreshServer(t, nil)

	if rec := post(t, srv, "/api/v1/auth/bootstrap", bootstrapBody); rec.Code != http.StatusCreated {
		t.Fatalf("bootstrap failed: %d", rec.Code)
	}

	rec := post(t, srv, "/api/v1/auth/login", bootstrapBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("the bootstrapped account could not sign in: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAnUnclaimedInstallationWarnsOnEveryStart(t *testing.T) {
	// Until it is claimed, whoever reaches this server first becomes its
	// administrator. That window is an accepted trade-off, so the least the
	// server can do is refuse to be quiet about it.
	buf := &bytes.Buffer{}
	logger, err := logging.New(logging.Options{Output: buf})
	if err != nil {
		t.Fatalf("building the logger failed: %v", err)
	}

	cfg := config.Default()
	cfg.RateLimit.Enabled = false
	db := storagetest.Open(t)
	srv, err := server.New(server.Options{
		Config: cfg, DB: db, Logger: logger, InstanceID: "net_TEST",
	})
	if err != nil {
		t.Fatalf("server.New failed: %v", err)
	}

	serveAndStop(t, srv)

	logged := buf.String()
	if !strings.Contains(logged, "no administrator") {
		t.Fatalf("an unclaimed installation did not warn on start:\n%s", logged)
	}
	if !strings.Contains(logged, `"level":"WARN"`) {
		t.Errorf("the unclaimed warning was not logged at WARN:\n%s", logged)
	}
}

func TestAClaimedInstallationDoesNotWarn(t *testing.T) {
	cfg := config.Default()
	cfg.RateLimit.Enabled = false
	db := storagetest.Open(t)

	claimer, err := server.New(server.Options{
		Config: cfg, DB: db, Logger: logging.Discard(), InstanceID: "net_TEST",
	})
	if err != nil {
		t.Fatalf("server.New failed: %v", err)
	}
	if rec := post(t, claimer, "/api/v1/auth/bootstrap", bootstrapBody); rec.Code != http.StatusCreated {
		t.Fatalf("bootstrap failed: %d %s", rec.Code, rec.Body.String())
	}

	buf := &bytes.Buffer{}
	logger, _ := logging.New(logging.Options{Output: buf})
	srv, err := server.New(server.Options{
		Config: cfg, DB: db, Logger: logger, InstanceID: "net_TEST",
	})
	if err != nil {
		t.Fatalf("server.New failed: %v", err)
	}

	serveAndStop(t, srv)

	if strings.Contains(buf.String(), "no administrator") {
		t.Fatalf("a claimed installation still warns about being unclaimed:\n%s", buf.String())
	}
}

// serveAndStop starts a server on an ephemeral port and shuts it straight
// down, which is enough to exercise the start-up path.
func serveAndStop(t *testing.T, srv *server.Server) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, listener) }()

	// Wait until it is actually serving before stopping it.
	url := "http://" + listener.Addr().String() + "/live"
	for range 100 {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not shut down")
	}
}
