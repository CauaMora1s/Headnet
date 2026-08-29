package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/headnet/headnet/internal/cliutil"
	"github.com/headnet/headnet/packages/api"
	"github.com/headnet/headnet/packages/protocol"
)

func runCLI(t *testing.T, argv ...string) (code int, stdout, stderr string) {
	t.Helper()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	ios := &cliutil.IO{In: strings.NewReader(""), Out: out, ErrOut: errOut}
	code = cliutil.Run(context.Background(), rootCommand(), ios, argv)
	return code, out.String(), errOut.String()
}

func TestVersionCommand(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runCLI(t, "version")

	if code != cliutil.ExitOK {
		t.Fatalf("exit code = %d (stderr %q)", code, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Fatal("version printed nothing")
	}
	for _, want := range []string{"commit", "built"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("version output is missing %q: %s", want, stdout)
		}
	}
}

func TestHelpListsTheWholeCommandTree(t *testing.T) {
	t.Parallel()
	code, stdout, _ := runCLI(t, "-h")

	if code != cliutil.ExitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, cmd := range []string{
		"version", "status", "diagnostics", "login", "logout", "up",
		"connect", "disconnect", "devices", "peers", "ping", "routes",
	} {
		if !strings.Contains(stdout, cmd) {
			t.Errorf("help does not list %q:\n%s", cmd, stdout)
		}
	}
}

func TestPlannedCommandsAreMarkedInHelp(t *testing.T) {
	t.Parallel()
	// A user reading the help must be able to tell what works today from what
	// is merely declared.
	_, stdout, _ := runCLI(t, "-h")
	if !strings.Contains(stdout, "planned") {
		t.Fatalf("planned commands are not marked as such:\n%s", stdout)
	}
}

func TestUnimplementedCommandsFailHonestly(t *testing.T) {
	t.Parallel()
	// This is the property that matters most in this build: a VPN client that
	// claims success while carrying no traffic could convince someone their
	// connection is protected when it is not.
	for _, cmd := range []string{
		"status", "login", "logout", "up", "connect", "disconnect",
		"devices", "peers", "ping", "routes",
	} {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := runCLI(t, cmd)

			if code != cliutil.ExitNotImplemented {
				t.Fatalf("%s exit code = %d, want %d", cmd, code, cliutil.ExitNotImplemented)
			}
			if code == cliutil.ExitOK {
				t.Fatalf("%s reported success for a feature that does not exist", cmd)
			}
			if !strings.Contains(stderr, "not implemented") {
				t.Errorf("%s does not say it is unimplemented:\n%s", cmd, stderr)
			}
			if !strings.Contains(stderr, "Phase") {
				t.Errorf("%s does not name the roadmap phase:\n%s", cmd, stderr)
			}
			if strings.TrimSpace(stdout) != "" {
				t.Errorf("%s wrote to stdout, where a script would read it as real output:\n%s", cmd, stdout)
			}
		})
	}
}

func TestNoCommandClaimsAConnection(t *testing.T) {
	t.Parallel()
	// Guard against a future stub that returns something like
	// {"connected": true} to make a UI look finished.
	for _, cmd := range []string{"status", "connect", "up", "peers"} {
		_, stdout, _ := runCLI(t, cmd)
		lowered := strings.ToLower(stdout)
		for _, claim := range []string{"connected", "\"connected\": true", "online"} {
			if strings.Contains(lowered, claim) {
				t.Errorf("%s claims %q while no VPN implementation exists:\n%s", cmd, claim, stdout)
			}
		}
	}
}

func TestDiagnosticsWithoutAServerSkipsNetworkChecks(t *testing.T) {
	t.Parallel()
	code, stdout, stderr := runCLI(t, "diagnostics")

	if code != cliutil.ExitOK {
		t.Fatalf("exit code = %d, want 0 when nothing failed (stderr %q)", code, stderr)
	}
	if !strings.Contains(stdout, "CLI build") {
		t.Errorf("diagnostics did not report the build:\n%s", stdout)
	}
	if !strings.Contains(stdout, "-server") {
		t.Errorf("diagnostics did not explain how to test a server:\n%s", stdout)
	}
}

func TestDiagnosticsNeverReportsUnbuiltSubsystemsAsPassing(t *testing.T) {
	t.Parallel()
	// Reporting an unimplemented subsystem as healthy is exactly the failure
	// this tool exists to prevent.
	code, stdout, _ := runCLI(t, "diagnostics", "-json")
	if code != cliutil.ExitOK {
		t.Fatalf("exit code = %d", code)
	}

	var report diagnosticsReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("JSON output did not parse: %v\n%s", err, stdout)
	}

	unbuilt := map[string]bool{
		"Client daemon":            true,
		"WireGuard interface":      true,
		"Direct peer connectivity": true,
		"Relay fallback":           true,
	}
	seen := 0
	for _, c := range report.Checks {
		if !unbuilt[c.Name] {
			continue
		}
		seen++
		if c.State == statePass {
			t.Errorf("%q reported as passing, but it is not implemented", c.Name)
		}
		if c.State != stateSkipped {
			t.Errorf("%q state = %q, want skipped", c.Name, c.State)
		}
		if !strings.Contains(c.Detail, "Phase") {
			t.Errorf("%q does not name the phase that will deliver it: %q", c.Name, c.Detail)
		}
	}
	if seen != len(unbuilt) {
		t.Fatalf("only %d of %d unbuilt subsystems were reported", seen, len(unbuilt))
	}
}

func TestDiagnosticsAgainstAHealthyServer(t *testing.T) {
	t.Parallel()
	srv := fakeControlServer(t, http.StatusOK, api.HealthResponse{
		Status:  api.StatusOK,
		Version: "0.1.0-test",
	}, api.VersionResponse{
		Version:            "0.1.0-test",
		ProtocolVersion:    protocol.Version,
		MinProtocolVersion: protocol.MinVersion,
	})

	code, stdout, stderr := runCLI(t, "diagnostics", "-server", srv.URL, "-json")
	if code != cliutil.ExitOK {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}

	report := parseReport(t, stdout)
	if got := report["Control server"]; got.State != statePass {
		t.Errorf("Control server = %q (%s), want pass", got.State, got.Detail)
	}
	if got := report["Protocol compatibility"]; got.State != statePass {
		t.Errorf("Protocol compatibility = %q (%s), want pass", got.State, got.Detail)
	}
}

func TestDiagnosticsDetectsAnIncompatibleServer(t *testing.T) {
	t.Parallel()
	// A server from a future major protocol version this build cannot speak.
	srv := fakeControlServer(t, http.StatusOK,
		api.HealthResponse{Status: api.StatusOK, Version: "9.0.0"},
		api.VersionResponse{Version: "9.0.0", ProtocolVersion: 9999, MinProtocolVersion: 9999})

	code, stdout, _ := runCLI(t, "diagnostics", "-server", srv.URL, "-json")
	if code != cliutil.ExitFailure {
		t.Fatalf("exit code = %d, want %d when a check fails", code, cliutil.ExitFailure)
	}

	got := parseReport(t, stdout)["Protocol compatibility"]
	if got.State != stateFail {
		t.Fatalf("Protocol compatibility = %q, want fail", got.State)
	}
	if got.Remedy == "" {
		t.Error("a failing check gave no remedy; that is an error message, not a diagnostic")
	}
}

func TestDiagnosticsDetectsAnUnhealthyServer(t *testing.T) {
	t.Parallel()
	srv := fakeControlServer(t, http.StatusServiceUnavailable,
		api.HealthResponse{Status: api.StatusDown, Version: "0.1.0-test"},
		api.VersionResponse{})

	code, stdout, _ := runCLI(t, "diagnostics", "-server", srv.URL, "-json")
	if code != cliutil.ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, cliutil.ExitFailure)
	}

	report := parseReport(t, stdout)
	if report["Control server"].State != stateFail {
		t.Errorf("Control server = %q, want fail", report["Control server"].State)
	}
	// Negotiation cannot run against a server that is not answering properly.
	if report["Protocol compatibility"].State != stateSkipped {
		t.Errorf("Protocol compatibility = %q, want skipped", report["Protocol compatibility"].State)
	}
}

func TestDiagnosticsDetectsAnUnreachableServer(t *testing.T) {
	t.Parallel()
	// Port 1 on the loopback interface: nothing listens there.
	code, stdout, _ := runCLI(t, "diagnostics", "-server", "http://127.0.0.1:1", "-json", "-timeout", "1s")
	if code != cliutil.ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, cliutil.ExitFailure)
	}

	got := parseReport(t, stdout)["Control server"]
	if got.State != stateFail {
		t.Fatalf("Control server = %q, want fail", got.State)
	}
	if !strings.Contains(strings.ToLower(got.Remedy), "firewall") {
		t.Errorf("the remedy does not mention the likely causes: %q", got.Remedy)
	}
}

func TestDiagnosticsRejectsAMalformedServerURL(t *testing.T) {
	t.Parallel()
	code, stdout, _ := runCLI(t, "diagnostics", "-server", "vpn.example.com", "-json")
	if code != cliutil.ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, cliutil.ExitFailure)
	}
	got := parseReport(t, stdout)["Control server"]
	if !strings.Contains(got.Remedy, "https://") {
		t.Errorf("the remedy does not show the expected form: %q", got.Remedy)
	}
}

func TestDiagnosticsSkipsDNSForALiteralAddress(t *testing.T) {
	t.Parallel()
	// Separating name resolution from reachability turns "it does not work"
	// into two distinct, differently-fixed problems; a literal IP has no name
	// to resolve.
	code, stdout, _ := runCLI(t, "diagnostics", "-server", "http://127.0.0.1:1", "-json", "-timeout", "1s")
	if code == cliutil.ExitOK {
		t.Fatal("expected the unreachable server to fail")
	}
	if got := parseReport(t, stdout)["DNS"]; got.State != stateSkipped {
		t.Fatalf("DNS = %q, want skipped for a literal IP address", got.State)
	}
}

func TestDiagnosticsRejectsPositionalArguments(t *testing.T) {
	t.Parallel()
	if code, _, _ := runCLI(t, "diagnostics", "unexpected"); code != cliutil.ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, cliutil.ExitUsage)
	}
}

func TestDiagnosticsHumanOutputIsReadable(t *testing.T) {
	t.Parallel()
	_, stdout, _ := runCLI(t, "diagnostics")

	if strings.Contains(stdout, "{") {
		t.Errorf("the default output looks like JSON:\n%s", stdout)
	}
	if !strings.Contains(stdout, "–") && !strings.Contains(stdout, "✓") {
		t.Errorf("the default output has no status markers:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Details:") {
		t.Errorf("the default output does not explain the skipped checks:\n%s", stdout)
	}
}

// fakeControlServer stands in for a control plane, answering the two endpoints
// diagnostics uses.
func fakeControlServer(t *testing.T, healthStatus int, health api.HealthResponse, version api.VersionResponse) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(healthStatus)
		_ = json.NewEncoder(w).Encode(health)
	})
	mux.HandleFunc("GET /api/v1/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(version)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// parseReport indexes a JSON diagnostics report by check name.
func parseReport(t *testing.T, stdout string) map[string]checkResult {
	t.Helper()
	var report diagnosticsReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("JSON output did not parse: %v\n%s", err, stdout)
	}
	byName := make(map[string]checkResult, len(report.Checks))
	for _, c := range report.Checks {
		byName[c.Name] = c
	}
	return byName
}
