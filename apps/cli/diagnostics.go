package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/headnet/headnet/internal/cliutil"
	"github.com/headnet/headnet/internal/version"
	"github.com/headnet/headnet/packages/api"
	"github.com/headnet/headnet/packages/protocol"
)

// checkState is the outcome of one diagnostic.
type checkState string

const (
	// statePass means the check ran and succeeded.
	statePass checkState = "pass"
	// stateFail means the check ran and found a real problem.
	stateFail checkState = "fail"
	// stateSkipped means the check could not run — either it needs an input
	// the user did not supply, or the feature it tests is not built yet.
	//
	// Skipped is deliberately distinct from pass. Reporting an unbuilt
	// subsystem as healthy is the exact failure mode this tool exists to
	// avoid.
	stateSkipped checkState = "skipped"
)

// symbol renders a state for a terminal.
func (s checkState) symbol() string {
	switch s {
	case statePass:
		return "✓"
	case stateFail:
		return "✗"
	default:
		return "–"
	}
}

// checkResult is one line of diagnostic output.
type checkResult struct {
	// Name is the subsystem being checked.
	Name string `json:"name"`
	// State is the outcome.
	State checkState `json:"state"`
	// Detail explains the outcome in one line.
	Detail string `json:"detail"`
	// Remedy is what the user should do about a failure. It is the difference
	// between a diagnostic and an error message.
	Remedy string `json:"remedy,omitempty"`
}

// diagnosticsReport is the machine-readable form of a run.
type diagnosticsReport struct {
	Version string        `json:"version"`
	Checks  []checkResult `json:"checks"`
	Failed  int           `json:"failed"`
}

func diagnosticsCommand() *cliutil.Command {
	var (
		serverURL string
		asJSON    bool
		timeout   time.Duration
	)

	return &cliutil.Command{
		Name:    "diagnostics",
		Summary: "check what is working and what is not",
		Usage:   "diagnostics [-server URL] [-json]",
		Long: "Runs every check this build can perform and explains each result.\n\n" +
			"Checks for subsystems that have not been built yet are reported as\n" +
			"skipped, never as passing. A green line here always means something\n" +
			"was actually verified.",
		SetFlags: func(fs *flag.FlagSet) {
			fs.StringVar(&serverURL, "server", "", "control server base URL to test, e.g. https://vpn.example.com")
			fs.BoolVar(&asJSON, "json", false, "emit machine-readable JSON")
			fs.DurationVar(&timeout, "timeout", 5*time.Second, "per-check network timeout")
		},
		Exec: func(ctx context.Context, ios *cliutil.IO, args []string) error {
			if len(args) > 0 {
				return cliutil.Errorf(cliutil.ExitUsage,
					"diagnostics takes no positional arguments (got %q)", strings.Join(args, " "))
			}

			checks := runDiagnostics(ctx, serverURL, timeout)

			failed := 0
			for _, c := range checks {
				if c.State == stateFail {
					failed++
				}
			}

			if asJSON {
				report := diagnosticsReport{Version: version.Get().Version, Checks: checks, Failed: failed}
				encoder := json.NewEncoder(ios.Out)
				encoder.SetIndent("", "  ")
				if err := encoder.Encode(report); err != nil {
					return err
				}
			} else {
				writeDiagnostics(ios, checks)
			}

			if failed > 0 {
				return &cliutil.ExitError{Code: cliutil.ExitFailure}
			}
			return nil
		},
	}
}

// runDiagnostics performs every check this build is capable of.
func runDiagnostics(ctx context.Context, serverURL string, timeout time.Duration) []checkResult {
	build := version.Get()

	checks := []checkResult{{
		Name:   "CLI build",
		State:  statePass,
		Detail: fmt.Sprintf("%s (%s, %s)", build.Version, build.Commit, build.Platform),
	}}

	checks = append(checks, checkControlServer(ctx, serverURL, timeout)...)

	// Everything below depends on the client daemon, which does not exist
	// yet. These are listed rather than hidden so the user can see the whole
	// shape of what a working installation will check.
	checks = append(checks,
		checkResult{
			Name:   "Client daemon",
			State:  stateSkipped,
			Detail: "not built yet (Phase 2)",
			Remedy: "The background daemon that holds this device's keys and manages WireGuard is not implemented.",
		},
		checkResult{
			Name:   "WireGuard interface",
			State:  stateSkipped,
			Detail: "not built yet (Phase 2)",
			Remedy: "No network interface is created by this build, so no traffic is carried.",
		},
		checkResult{
			Name:   "Direct peer connectivity",
			State:  stateSkipped,
			Detail: "not built yet (Phase 4)",
			Remedy: "NAT traversal — endpoint discovery, STUN and hole punching — has not been implemented.",
		},
		checkResult{
			Name:   "Relay fallback",
			State:  stateSkipped,
			Detail: "not built yet (Phase 5)",
			Remedy: "The relay used when a direct connection is impossible has not been implemented.",
		},
	)

	return checks
}

// checkControlServer probes a control plane, if the user named one.
func checkControlServer(ctx context.Context, serverURL string, timeout time.Duration) []checkResult {
	if strings.TrimSpace(serverURL) == "" {
		return []checkResult{{
			Name:   "Control server",
			State:  stateSkipped,
			Detail: "no server specified",
			Remedy: "Pass -server https://your-control-server to test reachability and version compatibility.",
		}}
	}

	base, err := url.Parse(serverURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return []checkResult{{
			Name:   "Control server",
			State:  stateFail,
			Detail: fmt.Sprintf("%q is not a valid URL", serverURL),
			Remedy: "Use a full URL including the scheme, for example https://vpn.example.com",
		}}
	}

	client := &http.Client{Timeout: timeout}
	results := []checkResult{checkDNS(ctx, base, timeout), checkReachable(ctx, client, base)}

	// Only worth negotiating if the server answered at all.
	if results[len(results)-1].State == statePass {
		results = append(results, checkProtocol(ctx, client, base))
	} else {
		results = append(results, checkResult{
			Name:   "Protocol compatibility",
			State:  stateSkipped,
			Detail: "the control server did not respond",
		})
	}
	return results
}

// checkDNS resolves the control server's hostname. Separating it from
// reachability turns "it does not work" into either "the name does not
// resolve" or "the name resolves but nothing answers", which are different
// problems with different fixes.
func checkDNS(ctx context.Context, base *url.URL, timeout time.Duration) checkResult {
	host := base.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		return checkResult{
			Name:   "DNS",
			State:  stateSkipped,
			Detail: "the server was given as a literal IP address, so no lookup was needed",
		}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return checkResult{
			Name:   "DNS",
			State:  stateFail,
			Detail: fmt.Sprintf("%s could not be resolved", host),
			Remedy: "Check the hostname spelling and this machine's DNS configuration.",
		}
	}
	return checkResult{
		Name:   "DNS",
		State:  statePass,
		Detail: fmt.Sprintf("%s resolves to %s", host, strings.Join(addrs, ", ")),
	}
}

// checkReachable calls the control server's health endpoint.
func checkReachable(ctx context.Context, client *http.Client, base *url.URL) checkResult {
	endpoint := base.JoinPath("/health").String()

	var health api.HealthResponse
	status, err := getJSON(ctx, client, endpoint, &health)
	if err != nil {
		return checkResult{
			Name:   "Control server",
			State:  stateFail,
			Detail: fmt.Sprintf("%s could not be reached", endpoint),
			Remedy: "Confirm the server is running, that the port is open, and that no firewall " +
				"or proxy is blocking outbound HTTPS to it.",
		}
	}

	// A 503 from /health is a live server reporting a broken dependency,
	// which is a different situation from an unreachable one.
	if status >= 500 {
		return checkResult{
			Name:   "Control server",
			State:  stateFail,
			Detail: fmt.Sprintf("reachable, but reporting %q", health.Status),
			Remedy: "The server is running but unhealthy. Check its logs and its database.",
		}
	}
	if status != http.StatusOK {
		return checkResult{
			Name:   "Control server",
			State:  stateFail,
			Detail: fmt.Sprintf("%s returned HTTP %d", endpoint, status),
			Remedy: "Confirm the URL points at a Headnet control server rather than another service.",
		}
	}

	return checkResult{
		Name:   "Control server",
		State:  statePass,
		Detail: fmt.Sprintf("%s is reachable and reports %q (version %s)", base.Host, health.Status, health.Version),
	}
}

// checkProtocol verifies that this build and the server can actually talk.
func checkProtocol(ctx context.Context, client *http.Client, base *url.URL) checkResult {
	endpoint := base.JoinPath(api.BasePath, "/version").String()

	var remote api.VersionResponse
	status, err := getJSON(ctx, client, endpoint, &remote)
	if err != nil || status != http.StatusOK {
		return checkResult{
			Name:   "Protocol compatibility",
			State:  stateFail,
			Detail: fmt.Sprintf("%s did not return version information", endpoint),
			Remedy: "The server may be older than this client, or the URL may not point at a Headnet server.",
		}
	}

	local := protocol.Local()
	remoteRange := protocol.Range{Version: remote.ProtocolVersion, Min: remote.MinProtocolVersion}
	if remoteRange.Min <= 0 {
		remoteRange.Min = remoteRange.Version
	}

	negotiated, _, err := protocol.Negotiate(local, remoteRange, nil, nil)
	if err != nil {
		return checkResult{
			Name:   "Protocol compatibility",
			State:  stateFail,
			Detail: err.Error(),
			Remedy: "Upgrade whichever side is older so their supported protocol ranges overlap.",
		}
	}

	return checkResult{
		Name:  "Protocol compatibility",
		State: statePass,
		Detail: fmt.Sprintf("agreed on protocol v%d (this build speaks %d–%d, the server speaks %d–%d)",
			negotiated, local.Min, local.Version, remoteRange.Min, remoteRange.Version),
	}
}

// getJSON performs a GET and decodes the body, returning the status code.
func getJSON(ctx context.Context, client *http.Client, endpoint string, dst any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "headnet-cli/"+version.Get().Version)

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	// A body is decoded even on an error status, because the health and error
	// envelopes both carry detail worth reporting.
	if dst != nil {
		_ = json.NewDecoder(resp.Body).Decode(dst)
	}
	return resp.StatusCode, nil
}

// writeDiagnostics renders the report for a human.
func writeDiagnostics(ios *cliutil.IO, checks []checkResult) {
	width := 0
	for _, c := range checks {
		width = max(width, len(c.Name))
	}

	fmt.Fprintln(ios.Out)
	for _, c := range checks {
		fmt.Fprintf(ios.Out, "  %s  %-*s  %s\n", c.State.symbol(), width, c.Name, c.Detail)
	}

	// Remedies are printed once, after the summary, so the list above stays
	// scannable while the explanations remain available.
	var notes []checkResult
	for _, c := range checks {
		if c.Remedy != "" && c.State != statePass {
			notes = append(notes, c)
		}
	}
	if len(notes) > 0 {
		fmt.Fprintln(ios.Out, "\nDetails:")
		for _, c := range notes {
			fmt.Fprintf(ios.Out, "  %s: %s\n", c.Name, c.Remedy)
		}
	}
	fmt.Fprintln(ios.Out)
}
