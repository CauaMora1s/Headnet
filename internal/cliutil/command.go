// Package cliutil is a small command tree for the Headnet command-line tools.
//
// It exists instead of a CLI framework because the standard library's flag
// package already does the hard part, and the remainder — dispatching a
// subcommand, printing help, choosing an exit code — is about a hundred lines.
// Adding a dependency on the path every operator command takes was not worth
// it for that. See docs/architecture/decisions/ADR-0011-cli-standard-library.md.
//
// Everything is written against injected streams so that command behaviour can
// be asserted in tests rather than eyeballed in a terminal.
package cliutil

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Exit codes. A CLI that always exits 0 or 1 cannot be scripted against, so
// these are part of the tool's contract and are documented in
// docs/user-guide/cli.md.
const (
	// ExitOK means the command succeeded.
	ExitOK = 0
	// ExitFailure means the command ran and failed.
	ExitFailure = 1
	// ExitUsage means the invocation was wrong: an unknown command, a bad
	// flag, or a missing argument.
	ExitUsage = 2
	// ExitNotImplemented means the command is a declared part of the CLI but
	// the feature behind it has not been built yet.
	ExitNotImplemented = 3
	// ExitUnavailable means a dependency the command needs — usually the
	// local daemon or the control server — could not be reached.
	ExitUnavailable = 4
)

// IO carries the streams a command reads and writes.
type IO struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer
}

// Command is one node of the command tree. A node either runs (Exec) or groups
// subcommands; a node with both runs only when invoked with no subcommand.
type Command struct {
	// Name is the word typed on the command line.
	Name string
	// Summary is the one-line description shown in the parent's help.
	Summary string
	// Usage is the argument syntax, e.g. "ping <device>".
	Usage string
	// Long is the extended help shown for this command alone.
	Long string
	// SetFlags registers flags. It is called before Exec, on a FlagSet scoped
	// to this command.
	SetFlags func(fs *flag.FlagSet)
	// Exec runs the command. args are the positional arguments left after
	// flag parsing.
	Exec func(ctx context.Context, io *IO, args []string) error
	// Subcommands are the children of this node.
	Subcommands []*Command
	// Hidden keeps a command out of the help listing without disabling it.
	Hidden bool
}

// ExitError lets a command choose its exit code while still returning an error.
type ExitError struct {
	Code int
	Err  error
}

// Error implements error.
func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return e.Err.Error()
}

// Unwrap exposes the underlying error to errors.Is and errors.As.
func (e *ExitError) Unwrap() error { return e.Err }

// Errorf returns an error that exits with the given code.
func Errorf(code int, format string, args ...any) error {
	return &ExitError{Code: code, Err: fmt.Errorf(format, args...)}
}

// NotImplemented returns the standard error for a command that is declared but
// not yet built.
//
// It names the roadmap phase rather than failing vaguely. A user who runs
// `headnet connect` today needs to know the feature does not exist, not to be
// left wondering whether their network is broken — and certainly not to be
// told the connection succeeded.
func NotImplemented(feature, phase string) error {
	return &ExitError{
		Code: ExitNotImplemented,
		Err: fmt.Errorf("%s is not implemented yet.\n\n"+
			"It is planned for %s of the roadmap:\n"+
			"  https://github.com/CauaMora1s/Headnet/blob/main/docs/ROADMAP.md", feature, phase),
	}
}

// Run dispatches argv against the command tree and returns a process exit code.
//
// argv excludes the program name, matching os.Args[1:].
func Run(ctx context.Context, root *Command, ios *IO, argv []string) int {
	cmd, args, path, err := resolve(root, argv)
	if err != nil {
		fmt.Fprintf(ios.ErrOut, "%s: %v\n\n", root.Name, err)
		writeHelp(ios.ErrOut, root, root.Name)
		return ExitUsage
	}

	fs := flag.NewFlagSet(path, flag.ContinueOnError)
	// Help output is written by writeHelp, which formats the whole command
	// tree consistently; the flag package's own usage text would not match.
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	if cmd.SetFlags != nil {
		cmd.SetFlags(fs)
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			writeHelp(ios.Out, cmd, path)
			return ExitOK
		}
		fmt.Fprintf(ios.ErrOut, "%s: %v\n\n", path, err)
		writeHelp(ios.ErrOut, cmd, path)
		return ExitUsage
	}

	if cmd.Exec == nil {
		// A grouping node invoked on its own: show what it contains rather
		// than failing with a bare error.
		writeHelp(ios.Out, cmd, path)
		if len(cmd.Subcommands) == 0 {
			return ExitFailure
		}
		return ExitUsage
	}

	if err := cmd.Exec(ctx, ios, fs.Args()); err != nil {
		var exit *ExitError
		if errors.As(err, &exit) {
			if exit.Err != nil {
				fmt.Fprintf(ios.ErrOut, "%s: %v\n", path, exit.Err)
			}
			return exit.Code
		}
		fmt.Fprintf(ios.ErrOut, "%s: %v\n", path, err)
		return ExitFailure
	}
	return ExitOK
}

// resolve walks the tree, returning the command to run, the arguments left for
// it, and the full path for help and error messages.
func resolve(root *Command, argv []string) (*Command, []string, string, error) {
	cmd := root
	path := root.Name

	for len(argv) > 0 {
		name := argv[0]
		// A flag terminates command resolution: everything from here belongs
		// to the command found so far.
		if strings.HasPrefix(name, "-") {
			break
		}
		child := cmd.lookup(name)
		if child == nil {
			if cmd == root {
				return nil, nil, "", fmt.Errorf("unknown command %q", name)
			}
			// Not a subcommand, so treat it as a positional argument.
			break
		}
		cmd = child
		path += " " + child.Name
		argv = argv[1:]
	}
	return cmd, argv, path, nil
}

func (c *Command) lookup(name string) *Command {
	for _, sub := range c.Subcommands {
		if sub.Name == name {
			return sub
		}
	}
	return nil
}

// writeHelp renders help for one command.
func writeHelp(w io.Writer, cmd *Command, path string) {
	if cmd.Long != "" {
		fmt.Fprintf(w, "%s\n\n", strings.TrimSpace(cmd.Long))
	} else if cmd.Summary != "" {
		fmt.Fprintf(w, "%s\n\n", cmd.Summary)
	}

	usage := cmd.Usage
	if usage == "" {
		usage = "[flags]"
		if len(cmd.Subcommands) > 0 {
			usage = "<command> [flags]"
		}
	}
	fmt.Fprintf(w, "Usage:\n  %s %s\n", path, usage)

	visible := make([]*Command, 0, len(cmd.Subcommands))
	for _, sub := range cmd.Subcommands {
		if !sub.Hidden {
			visible = append(visible, sub)
		}
	}
	if len(visible) > 0 {
		sort.Slice(visible, func(i, j int) bool { return visible[i].Name < visible[j].Name })

		width := 0
		for _, sub := range visible {
			width = max(width, len(sub.Name))
		}
		fmt.Fprintf(w, "\nCommands:\n")
		for _, sub := range visible {
			fmt.Fprintf(w, "  %-*s  %s\n", width, sub.Name, sub.Summary)
		}
		fmt.Fprintf(w, "\nRun \"%s <command> -h\" for more information about a command.\n", path)
	}

	if cmd.SetFlags != nil {
		fs := flag.NewFlagSet(path, flag.ContinueOnError)
		fs.SetOutput(w)
		cmd.SetFlags(fs)

		hasFlags := false
		fs.VisitAll(func(*flag.Flag) { hasFlags = true })
		if hasFlags {
			fmt.Fprintf(w, "\nFlags:\n")
			fs.PrintDefaults()
		}
	}
}
