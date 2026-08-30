// Command headnet-server runs the Headnet control plane.
//
// The control plane manages identity, devices, policy and network
// configuration. It never carries VPN traffic: that flows directly between
// peers over WireGuard, or through a relay, neither of which involves this
// process. See docs/architecture.md.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/CauaMora1s/Headnet/internal/config"
	"github.com/CauaMora1s/Headnet/internal/logging"
	"github.com/CauaMora1s/Headnet/internal/server"
	"github.com/CauaMora1s/Headnet/internal/storage"
	"github.com/CauaMora1s/Headnet/internal/version"
	pkgconfig "github.com/CauaMora1s/Headnet/packages/config"
	"github.com/CauaMora1s/Headnet/packages/shared"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		// The error is already phrased for an operator; no stack, no wrapping
		// noise, just what went wrong.
		fmt.Fprintf(os.Stderr, "headnet-server: %v\n", err)
		os.Exit(1)
	}
}

// run is main's testable body: it takes its arguments and streams explicitly
// and returns an error instead of calling os.Exit.
func run(argv []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("headnet-server", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		configPath  = fs.String("config", "", "path to the configuration file (optional; defaults and environment variables are used when absent)")
		showVersion = fs.Bool("version", false, "print the build version and exit")
		printConfig = fs.Bool("print-config", false, "print the effective configuration with secrets masked, then exit")
		migrateOnly = fs.Bool("migrate-only", false, "apply database migrations and exit without serving")
	)
	if err := fs.Parse(argv); err != nil {
		return err
	}

	if *showVersion {
		fmt.Fprintln(stdout, version.Get().String())
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		if errors.Is(err, pkgconfig.ErrFileNotFound) {
			// An explicitly requested file that is missing is a mistake worth
			// naming plainly, rather than silently falling back to defaults.
			return fmt.Errorf("%w\n\nCheck the -config path, or omit the flag to use defaults "+
				"and environment variables", err)
		}
		return err
	}

	if *printConfig {
		return printEffectiveConfig(cfg, stdout)
	}

	logger, err := logging.New(logging.Options{
		Level:     cfg.Log.Level,
		Format:    logging.Format(cfg.Log.Format),
		AddSource: cfg.Log.AddSource,
		Output:    stderr,
	})
	if err != nil {
		return err
	}

	// Cancelled on SIGINT or SIGTERM, which is what drives graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	build := version.Get()
	logger.InfoContext(ctx, "starting the Headnet control plane",
		"version", build.Version,
		"commit", build.Commit,
		"go_version", build.GoVersion,
		"platform", build.Platform,
		"environment", string(cfg.Environment),
		"http", cfg.Server.Listen,
		"database", string(cfg.Database.Driver),
	)

	db, err := storage.Open(ctx, storage.Options{
		Driver:          storage.Driver(cfg.Database.Driver),
		Path:            cfg.Database.Path,
		DSN:             cfg.Database.DSN,
		MaxOpenConns:    cfg.Database.MaxOpenConns,
		MaxIdleConns:    cfg.Database.MaxIdleConns,
		ConnMaxLifetime: cfg.Database.ConnMaxLifetime,
	})
	if err != nil {
		return err
	}
	defer db.Close()

	applied, err := storage.Migrate(ctx, db, logger)
	if err != nil {
		return err
	}
	if applied > 0 {
		logger.InfoContext(ctx, "database schema updated", "migrations_applied", applied)
	}

	if *migrateOnly {
		logger.InfoContext(ctx, "migrations complete; exiting because -migrate-only was set")
		return nil
	}

	instanceID, err := storage.InstanceID(ctx, db, shared.SystemClock.Now())
	if err != nil {
		return err
	}

	srv, err := server.New(server.Options{
		Config:     cfg,
		DB:         db,
		Logger:     logger,
		InstanceID: instanceID,
	})
	if err != nil {
		return err
	}

	if err := srv.Run(ctx); err != nil {
		return err
	}
	return nil
}

// printEffectiveConfig writes the configuration that would actually be used,
// with every secret masked.
//
// This is the answer to "why is it not picking up my setting?": it shows the
// result of defaults, file and environment together, which is otherwise
// invisible.
func printEffectiveConfig(cfg *config.Config, stdout *os.File) error {
	redacted, err := cfg.Redacted()
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(redacted)
}
