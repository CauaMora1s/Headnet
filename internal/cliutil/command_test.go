package cliutil_test

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/CauaMora1s/Headnet/internal/cliutil"
)

// run dispatches argv and returns the exit code plus both streams.
func run(t *testing.T, root *cliutil.Command, argv ...string) (code int, stdout, stderr string) {
	t.Helper()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	ios := &cliutil.IO{In: strings.NewReader(""), Out: out, ErrOut: errOut}
	code = cliutil.Run(context.Background(), root, ios, argv)
	return code, out.String(), errOut.String()
}

// tree builds a small command tree covering every dispatch shape.
func tree(t *testing.T) (*cliutil.Command, *[]string) {
	t.Helper()
	var calls []string

	return &cliutil.Command{
		Name:    "headnet",
		Summary: "Headnet command-line interface",
		Subcommands: []*cliutil.Command{
			{
				Name:    "status",
				Summary: "show connection status",
				Exec: func(_ context.Context, io *cliutil.IO, args []string) error {
					calls = append(calls, "status:"+strings.Join(args, ","))
					return nil
				},
			},
			{
				Name:    "ping",
				Summary: "ping a device",
				Usage:   "ping <device>",
				Exec: func(_ context.Context, io *cliutil.IO, args []string) error {
					if len(args) != 1 {
						return cliutil.Errorf(cliutil.ExitUsage, "ping needs exactly one device")
					}
					calls = append(calls, "ping:"+args[0])
					return nil
				},
			},
			{
				Name:    "connect",
				Summary: "connect to the network",
				Exec: func(context.Context, *cliutil.IO, []string) error {
					return cliutil.NotImplemented("connecting to a network", "Phase 2")
				},
			},
			{
				Name:    "boom",
				Summary: "always fails",
				Exec: func(context.Context, *cliutil.IO, []string) error {
					return errors.New("something went wrong")
				},
			},
			{
				Name:    "flagged",
				Summary: "takes flags",
				SetFlags: func(fs *flag.FlagSet) {
					fs.Bool("json", false, "emit JSON")
					fs.String("server", "", "control server URL")
				},
				Exec: func(context.Context, *cliutil.IO, []string) error {
					calls = append(calls, "flagged")
					return nil
				},
			},
			{
				Name:    "admin",
				Summary: "administrative commands",
				Subcommands: []*cliutil.Command{
					{
						Name:    "users",
						Summary: "manage users",
						Exec: func(_ context.Context, io *cliutil.IO, args []string) error {
							calls = append(calls, "admin users:"+strings.Join(args, ","))
							return nil
						},
					},
				},
			},
			{
				Name:    "internal",
				Summary: "hidden helper",
				Hidden:  true,
				Exec:    func(context.Context, *cliutil.IO, []string) error { return nil },
			},
		},
	}, &calls
}

func TestDispatchesASubcommand(t *testing.T) {
	root, calls := tree(t)
	code, _, stderr := run(t, root, "status")

	if code != cliutil.ExitOK {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	if len(*calls) != 1 || (*calls)[0] != "status:" {
		t.Fatalf("calls = %v, want the status command to have run", *calls)
	}
}

func TestDispatchesANestedSubcommand(t *testing.T) {
	root, calls := tree(t)
	code, _, stderr := run(t, root, "admin", "users", "list")

	if code != cliutil.ExitOK {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", code, stderr)
	}
	if len(*calls) != 1 || (*calls)[0] != "admin users:list" {
		t.Fatalf("calls = %v, want the nested command to have run with its argument", *calls)
	}
}

func TestPositionalArgumentsReachTheCommand(t *testing.T) {
	root, calls := tree(t)
	if code, _, stderr := run(t, root, "ping", "laptop"); code != cliutil.ExitOK {
		t.Fatalf("exit code = %d (stderr %q)", code, stderr)
	}
	if (*calls)[0] != "ping:laptop" {
		t.Fatalf("calls = %v, want the positional argument to be forwarded", *calls)
	}
}

func TestExitCodes(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want int
	}{
		{"success", []string{"status"}, cliutil.ExitOK},
		{"plain failure", []string{"boom"}, cliutil.ExitFailure},
		{"usage error from a command", []string{"ping"}, cliutil.ExitUsage},
		{"unknown command", []string{"teleport"}, cliutil.ExitUsage},
		{"unknown flag", []string{"flagged", "-nope"}, cliutil.ExitUsage},
		{"not implemented", []string{"connect"}, cliutil.ExitNotImplemented},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, _ := tree(t)
			if code, _, _ := run(t, root, tt.argv...); code != tt.want {
				t.Fatalf("exit code = %d, want %d", code, tt.want)
			}
		})
	}
}

func TestNotImplementedIsHonestAndActionable(t *testing.T) {
	// A user running `headnet connect` today needs to be told the feature
	// does not exist — not left wondering whether their network is broken,
	// and certainly not told the connection succeeded.
	root, _ := tree(t)
	code, stdout, stderr := run(t, root, "connect")

	if code != cliutil.ExitNotImplemented {
		t.Fatalf("exit code = %d, want %d", code, cliutil.ExitNotImplemented)
	}
	if !strings.Contains(stderr, "not implemented") {
		t.Errorf("stderr does not say the feature is unimplemented:\n%s", stderr)
	}
	if !strings.Contains(stderr, "Phase 2") {
		t.Errorf("stderr does not name the roadmap phase:\n%s", stderr)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("an unimplemented command wrote to stdout, where a script would read it as output:\n%s", stdout)
	}
}

func TestUnknownCommandSuggestsWhatIsAvailable(t *testing.T) {
	root, _ := tree(t)
	code, _, stderr := run(t, root, "teleport")

	if code != cliutil.ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, cliutil.ExitUsage)
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Errorf("stderr does not name the problem:\n%s", stderr)
	}
	if !strings.Contains(stderr, "status") {
		t.Errorf("stderr does not list the available commands:\n%s", stderr)
	}
}

func TestHelpGoesToStdoutAndExitsZero(t *testing.T) {
	// `headnet -h | less` has to work, so help is output rather than an error.
	root, _ := tree(t)
	code, stdout, _ := run(t, root, "-h")

	if code != cliutil.ExitOK {
		t.Fatalf("exit code = %d, want 0 for an explicit help request", code)
	}
	if !strings.Contains(stdout, "Usage:") || !strings.Contains(stdout, "status") {
		t.Fatalf("help output is missing usage or the command list:\n%s", stdout)
	}
}

func TestSubcommandHelp(t *testing.T) {
	root, _ := tree(t)
	code, stdout, _ := run(t, root, "flagged", "-h")

	if code != cliutil.ExitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "-json") || !strings.Contains(stdout, "-server") {
		t.Fatalf("subcommand help does not list its flags:\n%s", stdout)
	}
}

func TestHiddenCommandsRunButAreNotListed(t *testing.T) {
	root, _ := tree(t)

	_, stdout, _ := run(t, root, "-h")
	if strings.Contains(stdout, "hidden helper") {
		t.Errorf("a hidden command appeared in the help listing:\n%s", stdout)
	}
	if code, _, _ := run(t, root, "internal"); code != cliutil.ExitOK {
		t.Fatalf("a hidden command could not be invoked: exit %d", code)
	}
}

func TestGroupingNodeShowsItsSubcommands(t *testing.T) {
	root, _ := tree(t)
	code, stdout, _ := run(t, root, "admin")

	if code != cliutil.ExitUsage {
		t.Fatalf("exit code = %d, want %d for a group invoked with no subcommand", code, cliutil.ExitUsage)
	}
	if !strings.Contains(stdout, "users") {
		t.Fatalf("the group did not list its subcommands:\n%s", stdout)
	}
}

func TestFlagsAreParsedBeforeExec(t *testing.T) {
	var gotJSON bool
	root := &cliutil.Command{
		Name: "headnet",
		Subcommands: []*cliutil.Command{{
			Name: "list",
			SetFlags: func(fs *flag.FlagSet) {
				fs.Bool("json", false, "emit JSON")
			},
			Exec: func(context.Context, *cliutil.IO, []string) error { return nil },
		}},
	}
	// Rebuild with a closure that captures the parsed value.
	root.Subcommands[0].SetFlags = func(fs *flag.FlagSet) {
		fs.BoolVar(&gotJSON, "json", false, "emit JSON")
	}

	if code, _, stderr := run(t, root, "list", "-json"); code != cliutil.ExitOK {
		t.Fatalf("exit code = %d (stderr %q)", code, stderr)
	}
	if !gotJSON {
		t.Fatal("the -json flag was not parsed before Exec ran")
	}
}

func TestFlagsMayFollowANestedCommand(t *testing.T) {
	var format string
	root := &cliutil.Command{
		Name: "headnet",
		Subcommands: []*cliutil.Command{{
			Name: "admin",
			Subcommands: []*cliutil.Command{{
				Name:     "users",
				SetFlags: func(fs *flag.FlagSet) { fs.StringVar(&format, "format", "text", "output format") },
				Exec:     func(context.Context, *cliutil.IO, []string) error { return nil },
			}},
		}},
	}

	if code, _, stderr := run(t, root, "admin", "users", "-format", "json"); code != cliutil.ExitOK {
		t.Fatalf("exit code = %d (stderr %q)", code, stderr)
	}
	if format != "json" {
		t.Fatalf("format = %q, want json", format)
	}
}

func TestExitErrorUnwraps(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("underlying cause")
	err := error(&cliutil.ExitError{Code: cliutil.ExitUnavailable, Err: sentinel})

	if !errors.Is(err, sentinel) {
		t.Fatal("ExitError does not unwrap to its cause")
	}
	var exit *cliutil.ExitError
	if !errors.As(err, &exit) || exit.Code != cliutil.ExitUnavailable {
		t.Fatalf("errors.As did not recover the exit code: %+v", exit)
	}
}

func TestExitErrorWithoutACauseStillReportsACode(t *testing.T) {
	t.Parallel()
	err := &cliutil.ExitError{Code: cliutil.ExitUnavailable}
	if err.Error() == "" {
		t.Fatal("ExitError with no cause produced an empty message")
	}
}

func TestErrorsGoToStderrNotStdout(t *testing.T) {
	// Anything on stdout is the command's output; a script piping it must not
	// receive error text.
	root, _ := tree(t)
	_, stdout, stderr := run(t, root, "boom")

	if strings.TrimSpace(stdout) != "" {
		t.Errorf("error output leaked to stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "something went wrong") {
		t.Errorf("the error did not reach stderr:\n%s", stderr)
	}
}
