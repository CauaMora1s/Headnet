// Command headnet is the Headnet command-line interface.
//
// Most of the commands below are declared but not yet implemented: the client
// daemon they talk to arrives in Phase 2 of docs/ROADMAP.md. They are present
// so the shape of the tool is visible and stable, and every one of them says
// plainly that it is unimplemented rather than pretending to succeed.
//
// That distinction matters more here than in most software. A VPN client that
// reports "connected" when nothing is running does not merely mislead a user;
// it can convince them their traffic is protected while it is in the clear.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/CauaMora1s/Headnet/internal/cliutil"
	"github.com/CauaMora1s/Headnet/internal/version"
)

func main() {
	// Ctrl-C cancels the in-flight command rather than killing the process
	// mid-write, so a command that is partway through changing local state
	// can unwind.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ios := &cliutil.IO{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr}
	os.Exit(cliutil.Run(ctx, rootCommand(), ios, os.Args[1:]))
}

// rootCommand builds the command tree.
func rootCommand() *cliutil.Command {
	return &cliutil.Command{
		Name:    "headnet",
		Summary: "Manage this device's Headnet private network",
		Long: "headnet manages this device's membership of a Headnet private network.\n\n" +
			"This is an early build. Commands marked below as planned are not\n" +
			"implemented yet and will tell you so rather than pretending to work.",
		Subcommands: []*cliutil.Command{
			versionCommand(),
			diagnosticsCommand(),
			statusCommand(),

			// Phase 2: the client daemon, enrollment and the WireGuard
			// interface.
			planned("login", "authenticate this device with a control server", "logging in", "Phase 2"),
			planned("logout", "sign out and forget local credentials", "logging out", "Phase 2"),
			planned("up", "bring the network up, optionally with a setup key", "bringing the network up", "Phase 2"),
			planned("connect", "connect to the private network", "connecting to a network", "Phase 2"),
			planned("disconnect", "disconnect from the private network", "disconnecting", "Phase 2"),

			// Phase 3: peer distribution and device-to-device connectivity.
			planned("devices", "list the devices on this network", "listing devices", "Phase 3"),
			planned("peers", "show peer connection detail", "showing peers", "Phase 3"),
			planned("ping", "check reachability of another device", "pinging a device", "Phase 3"),

			// Phase 8: subnet routes.
			planned("routes", "manage advertised subnet routes", "route management", "Phase 8"),
		},
	}
}

// versionCommand prints the build identity. It works today and is the first
// thing to include in a bug report.
func versionCommand() *cliutil.Command {
	return &cliutil.Command{
		Name:    "version",
		Summary: "print the build version",
		Exec: func(_ context.Context, ios *cliutil.IO, _ []string) error {
			fmt.Fprintln(ios.Out, version.Get().String())
			return nil
		},
	}
}

// statusCommand reports the state of the local daemon.
//
// There is no daemon yet, so rather than inventing a plausible "disconnected"
// state it says exactly that. "Disconnected" would be a claim about a running
// system; "not built yet" is the truth.
func statusCommand() *cliutil.Command {
	return &cliutil.Command{
		Name:    "status",
		Summary: "show this device's connection status",
		Long: "Shows whether this device is connected, which peers it can reach,\n" +
			"and how each connection is carried.\n\n" +
			"This requires the client daemon, which is not built yet.",
		Exec: func(context.Context, *cliutil.IO, []string) error {
			return cliutil.NotImplemented("connection status", "Phase 2")
		},
	}
}

// planned declares a command that exists in the tree but not yet in the
// product, so that `headnet -h` shows the real shape of the tool.
func planned(name, summary, feature, phase string) *cliutil.Command {
	return &cliutil.Command{
		Name:    name,
		Summary: summary + " (planned, " + phase + ")",
		Exec: func(context.Context, *cliutil.IO, []string) error {
			return cliutil.NotImplemented(feature, phase)
		},
	}
}
