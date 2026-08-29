// SPDX-License-Identifier: Apache-2.0
// Copyright The Headnet Authors

package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/headnet/headnet/packages/config"
)

type dbSection struct {
	Driver   string `yaml:"driver" env:"DATABASE_DRIVER"`
	Path     string `yaml:"path" env:"DATABASE_PATH"`
	Password string `yaml:"password" env:"DATABASE_PASSWORD" secret:"true"`
	MaxConns int    `yaml:"max_conns" env:"DATABASE_MAX_CONNS"`
}

type testConfig struct {
	Listen    string        `yaml:"listen" env:"LISTEN"`
	Debug     bool          `yaml:"debug" env:"DEBUG"`
	Timeout   time.Duration `yaml:"timeout" env:"TIMEOUT"`
	Providers []string      `yaml:"providers" env:"PROVIDERS"`
	Ratio     float64       `yaml:"ratio" env:"RATIO"`
	Workers   uint16        `yaml:"workers" env:"WORKERS"`
	Database  dbSection     `yaml:"database"`

	validateErr error
}

func (c *testConfig) Validate() error { return c.validateErr }

func defaults() *testConfig {
	return &testConfig{
		Listen:    ":8080",
		Timeout:   30 * time.Second,
		Providers: []string{"local"},
		Ratio:     1.5,
		Workers:   4,
		Database:  dbSection{Driver: "sqlite", Path: "./data/headnet.db", MaxConns: 10},
	}
}

// env builds a Getenv function backed by a map, so tests never mutate the real
// process environment and can safely run in parallel.
func env(pairs map[string]string) func(string) string {
	return func(key string) string { return pairs[key] }
}

func writeFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing the fixture failed: %v", err)
	}
	return path
}

func TestDefaultsSurviveWhenNothingOverridesThem(t *testing.T) {
	t.Parallel()
	cfg := defaults()
	if err := config.Load("", cfg, config.Options{}); err != nil {
		t.Fatalf("Load returned an unexpected error: %v", err)
	}
	if cfg.Listen != ":8080" || cfg.Database.Driver != "sqlite" || cfg.Timeout != 30*time.Second {
		t.Fatalf("defaults were altered: %+v", cfg)
	}
}

func TestFileOverridesDefaults(t *testing.T) {
	t.Parallel()
	path := writeFile(t, "listen: \":9090\"\ndebug: true\ndatabase:\n  driver: postgres\n")

	cfg := defaults()
	if err := config.Load(path, cfg, config.Options{}); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Listen != ":9090" {
		t.Errorf("listen = %q, want :9090", cfg.Listen)
	}
	if !cfg.Debug {
		t.Error("debug = false, want true")
	}
	if cfg.Database.Driver != "postgres" {
		t.Errorf("database.driver = %q, want postgres", cfg.Database.Driver)
	}
	// Keys the file did not mention keep their defaults instead of being zeroed.
	if cfg.Database.Path != "./data/headnet.db" {
		t.Errorf("database.path = %q, want the default to survive", cfg.Database.Path)
	}
	if cfg.Database.MaxConns != 10 {
		t.Errorf("database.max_conns = %d, want the default to survive", cfg.Database.MaxConns)
	}
}

func TestEnvironmentOverridesFile(t *testing.T) {
	t.Parallel()
	path := writeFile(t, "listen: \":9090\"\ndatabase:\n  driver: postgres\n")

	cfg := defaults()
	err := config.Load(path, cfg, config.Options{
		EnvPrefix: "HEADNET",
		Getenv: env(map[string]string{
			"HEADNET_LISTEN":          ":7070",
			"HEADNET_DATABASE_DRIVER": "sqlite",
		}),
	})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Listen != ":7070" {
		t.Errorf("listen = %q, want the environment to win over the file", cfg.Listen)
	}
	if cfg.Database.Driver != "sqlite" {
		t.Errorf("database.driver = %q, want the environment to win over the file", cfg.Database.Driver)
	}
}

func TestEnvironmentParsesEverySupportedType(t *testing.T) {
	t.Parallel()
	cfg := defaults()
	err := config.Load("", cfg, config.Options{
		EnvPrefix: "HEADNET",
		Getenv: env(map[string]string{
			"HEADNET_DEBUG":              "true",
			"HEADNET_TIMEOUT":            "2m30s",
			"HEADNET_PROVIDERS":          "local, oidc , github",
			"HEADNET_RATIO":              "0.25",
			"HEADNET_WORKERS":            "16",
			"HEADNET_DATABASE_MAX_CONNS": "42",
		}),
	})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !cfg.Debug {
		t.Error("bool was not parsed")
	}
	if cfg.Timeout != 150*time.Second {
		t.Errorf("timeout = %v, want 2m30s", cfg.Timeout)
	}
	if len(cfg.Providers) != 3 || cfg.Providers[1] != "oidc" {
		t.Errorf("providers = %v, want the list split on commas and trimmed", cfg.Providers)
	}
	if cfg.Ratio != 0.25 {
		t.Errorf("ratio = %v, want 0.25", cfg.Ratio)
	}
	if cfg.Workers != 16 {
		t.Errorf("workers = %d, want 16", cfg.Workers)
	}
	if cfg.Database.MaxConns != 42 {
		t.Errorf("database.max_conns = %d, want 42", cfg.Database.MaxConns)
	}
}

func TestEmptyEnvironmentVariableIsTreatedAsUnset(t *testing.T) {
	t.Parallel()
	// Exporting an empty HEADNET_LISTEN in a shell must not blank out the
	// listen address; that is nearly always an accident rather than intent.
	cfg := defaults()
	err := config.Load("", cfg, config.Options{
		EnvPrefix: "HEADNET",
		Getenv:    env(map[string]string{"HEADNET_LISTEN": "   "}),
	})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Listen != ":8080" {
		t.Fatalf("listen = %q, want the default to survive an empty variable", cfg.Listen)
	}
}

func TestUnknownYAMLKeyIsRejected(t *testing.T) {
	t.Parallel()
	// A typo in a security-relevant key must fail loudly instead of silently
	// leaving a permissive default in place.
	path := writeFile(t, "lisen: \":9090\"\n")
	if err := config.Load(path, defaults(), config.Options{}); err == nil {
		t.Fatal("Load accepted an unknown configuration key")
	}
}

func TestUnknownNestedYAMLKeyIsRejected(t *testing.T) {
	t.Parallel()
	path := writeFile(t, "database:\n  drivr: postgres\n")
	if err := config.Load(path, defaults(), config.Options{}); err == nil {
		t.Fatal("Load accepted an unknown key inside a nested section")
	}
}

func TestMissingFileIsReportedDistinctly(t *testing.T) {
	t.Parallel()
	err := config.Load(filepath.Join(t.TempDir(), "absent.yaml"), defaults(), config.Options{})
	if !errors.Is(err, config.ErrFileNotFound) {
		t.Fatalf("Load error = %v, want it to wrap ErrFileNotFound", err)
	}
}

func TestEmptyFileMeansUseEveryDefault(t *testing.T) {
	t.Parallel()
	cfg := defaults()
	if err := config.Load(writeFile(t, "\n  \n"), cfg, config.Options{}); err != nil {
		t.Fatalf("Load rejected an empty file: %v", err)
	}
	if cfg.Listen != ":8080" {
		t.Fatalf("listen = %q, want the default", cfg.Listen)
	}
}

func TestMalformedEnvironmentValueNamesTheVariable(t *testing.T) {
	t.Parallel()
	err := config.Load("", defaults(), config.Options{
		EnvPrefix: "HEADNET",
		Getenv:    env(map[string]string{"HEADNET_TIMEOUT": "half an hour"}),
	})
	if err == nil {
		t.Fatal("Load accepted a malformed duration")
	}
	if !strings.Contains(err.Error(), "HEADNET_TIMEOUT") {
		t.Fatalf("error %q does not name the offending variable", err)
	}
}

func TestValidateRunsAfterEveryLayer(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("listen address is reserved")
	cfg := defaults()
	cfg.validateErr = sentinel

	err := config.Load("", cfg, config.Options{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Load error = %v, want it to wrap the validation error", err)
	}
}

func TestLoadRejectsInvalidDestinations(t *testing.T) {
	t.Parallel()
	if err := config.Load("", testConfig{}, config.Options{}); err == nil {
		t.Error("Load accepted a non-pointer destination")
	}
	if err := config.Load("", (*testConfig)(nil), config.Options{}); err == nil {
		t.Error("Load accepted a nil pointer")
	}
	s := "not a struct"
	if err := config.Load("", &s, config.Options{}); err == nil {
		t.Error("Load accepted a pointer to a non-struct")
	}
}

func TestApplyEnvIsANoOpWithoutAPrefix(t *testing.T) {
	t.Parallel()
	cfg := defaults()
	err := config.ApplyEnv(cfg, config.Options{
		Getenv: env(map[string]string{"HEADNET_LISTEN": ":1234"}),
	})
	if err != nil {
		t.Fatalf("ApplyEnv failed: %v", err)
	}
	if cfg.Listen != ":8080" {
		t.Fatalf("listen = %q, want no environment overlay without a prefix", cfg.Listen)
	}
}
