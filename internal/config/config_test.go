package config_test

import (
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/headnet/headnet/internal/config"
)

// mutate returns a valid default configuration with one change applied, which
// keeps each validation case focused on the single field under test.
func mutate(fn func(*config.Config)) *config.Config {
	cfg := config.Default()
	if fn != nil {
		fn(cfg)
	}
	return cfg
}

func TestDefaultConfigurationIsValid(t *testing.T) {
	t.Parallel()
	if err := config.Default().Validate(); err != nil {
		t.Fatalf("the shipped defaults do not validate: %v", err)
	}
}

func TestDefaultIPv4PoolSitsInCarrierGradeNATRange(t *testing.T) {
	t.Parallel()
	// 100.64.0.0/10 is the conventional overlay range precisely because it is
	// almost never present on a home or office LAN. Drifting out of it would
	// make address collisions common for users.
	cgnat := netip.MustParsePrefix("100.64.0.0/10")
	pool := config.Default().Network.IPv4Pool()
	if !cgnat.Overlaps(pool) || pool.Bits() < cgnat.Bits() {
		t.Fatalf("default IPv4 pool %s is not contained in %s", pool, cgnat)
	}
}

func TestDefaultIPv6PoolIsUniqueLocal(t *testing.T) {
	t.Parallel()
	pool, enabled := config.Default().Network.IPv6Pool()
	if !enabled {
		t.Fatal("IPv6 is disabled by default; the default should hand out a ULA prefix")
	}
	if ula := netip.MustParsePrefix("fc00::/7"); !ula.Overlaps(pool) {
		t.Fatalf("default IPv6 pool %s is not a unique-local prefix", pool)
	}
}

func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	t.Parallel()
	// An operator editing a file over SSH should not have to restart the
	// server once per typo.
	cfg := mutate(func(c *config.Config) {
		c.Server.Listen = "not-an-address"
		c.Database.Driver = "mysql"
		c.Network.IPv4CIDR = "nonsense"
		c.Log.Level = "chatty"
	})

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate accepted a configuration with four independent faults")
	}
	for _, want := range []string{"server.listen", "database.driver", "network.ipv4_cidr", "log.level"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the aggregated error does not mention %q:\n%v", want, err)
		}
	}
}

func TestServerValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*config.Config)
		wantErr string
	}{
		{"empty listen", func(c *config.Config) { c.Server.Listen = "" }, "server.listen"},
		{"listen without a port", func(c *config.Config) { c.Server.Listen = "localhost" }, "server.listen"},
		{"listen with an empty port", func(c *config.Config) { c.Server.Listen = "localhost:" }, "server.listen"},
		{"empty base url", func(c *config.Config) { c.Server.BaseURL = "" }, "server.base_url"},
		{"base url with a bad scheme", func(c *config.Config) { c.Server.BaseURL = "ftp://example.com" }, "server.base_url"},
		{"base url without a host", func(c *config.Config) { c.Server.BaseURL = "https://" }, "server.base_url"},
		{"zero read timeout", func(c *config.Config) { c.Server.ReadTimeout = 0 }, "server.read_timeout"},
		{"negative write timeout", func(c *config.Config) { c.Server.WriteTimeout = -time.Second }, "server.write_timeout"},
		{"zero idle timeout", func(c *config.Config) { c.Server.IdleTimeout = 0 }, "server.idle_timeout"},
		{"zero shutdown timeout", func(c *config.Config) { c.Server.ShutdownTimeout = 0 }, "server.shutdown_timeout"},
		{"zero body limit", func(c *config.Config) { c.Server.MaxBodyBytes = 0 }, "server.max_body_bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := mutate(tt.mutate).Validate()
			if err == nil {
				t.Fatalf("Validate accepted an invalid %s", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not name %q", err, tt.wantErr)
			}
		})
	}
}

func TestListenBindingEveryInterfaceIsAccepted(t *testing.T) {
	t.Parallel()
	for _, listen := range []string{":8080", "0.0.0.0:8080", "127.0.0.1:8080", "[::1]:8080"} {
		if err := mutate(func(c *config.Config) { c.Server.Listen = listen }).Validate(); err != nil {
			t.Errorf("Validate rejected the valid listen address %q: %v", listen, err)
		}
	}
}

func TestPlainHTTPIsRefusedInProduction(t *testing.T) {
	t.Parallel()
	// Enrollment credentials and session cookies would travel in the clear.
	cfg := mutate(func(c *config.Config) {
		c.Environment = config.EnvProduction
		c.Server.BaseURL = "http://vpn.example.com"
	})

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate accepted a plain-HTTP base URL in production")
	}
	if !strings.Contains(err.Error(), "allow_insecure_http") {
		t.Fatalf("the error does not tell the operator how to proceed deliberately:\n%v", err)
	}
}

func TestPlainHTTPInProductionNeedsAnExplicitOptIn(t *testing.T) {
	t.Parallel()
	// The escape hatch exists for TLS terminated by a proxy this server
	// cannot see, but it has to be chosen on purpose.
	cfg := mutate(func(c *config.Config) {
		c.Environment = config.EnvProduction
		c.Server.BaseURL = "http://vpn.example.com"
		c.Server.AllowInsecureHTTP = true
	})
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate rejected an explicit opt-in: %v", err)
	}
}

func TestPlainHTTPIsFineInDevelopment(t *testing.T) {
	t.Parallel()
	cfg := mutate(func(c *config.Config) { c.Server.BaseURL = "http://localhost:8080" })
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate rejected plain HTTP on a developer machine: %v", err)
	}
}

func TestHTTPSIsAlwaysAccepted(t *testing.T) {
	t.Parallel()
	cfg := mutate(func(c *config.Config) {
		c.Environment = config.EnvProduction
		c.Server.BaseURL = "https://vpn.example.com"
	})
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate rejected an HTTPS base URL in production: %v", err)
	}
}

func TestEnvironmentValidation(t *testing.T) {
	t.Parallel()
	for _, env := range []config.Environment{config.EnvDevelopment, config.EnvProduction} {
		err := mutate(func(c *config.Config) {
			c.Environment = env
			// Production additionally requires a secure base URL; that rule
			// has its own tests, so keep this case focused on the enum.
			c.Server.BaseURL = "https://vpn.example.com"
		}).Validate()
		if err != nil {
			t.Errorf("Validate rejected the valid environment %q: %v", env, err)
		}
	}
	for _, env := range []config.Environment{"", "staging", "PRODUCTION"} {
		if err := mutate(func(c *config.Config) { c.Environment = env }).Validate(); err == nil {
			t.Errorf("Validate accepted the invalid environment %q", env)
		}
	}
}

func TestDatabaseValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(*config.Config)
		wantErr string
	}{
		{
			name:    "sqlite without a path",
			mutate:  func(c *config.Config) { c.Database.Path = "  " },
			wantErr: "database.path",
		},
		{
			name: "postgres without a dsn",
			mutate: func(c *config.Config) {
				c.Database.Driver = config.DriverPostgres
				c.Database.DSN = ""
			},
			wantErr: "database.dsn",
		},
		{
			name:    "unknown driver",
			mutate:  func(c *config.Config) { c.Database.Driver = "mysql" },
			wantErr: "database.driver",
		},
		{
			name:    "empty driver",
			mutate:  func(c *config.Config) { c.Database.Driver = "" },
			wantErr: "database.driver",
		},
		{
			name:    "no connections allowed",
			mutate:  func(c *config.Config) { c.Database.MaxOpenConns = 0 },
			wantErr: "database.max_open_conns",
		},
		{
			name:    "negative idle connections",
			mutate:  func(c *config.Config) { c.Database.MaxIdleConns = -1 },
			wantErr: "database.max_idle_conns",
		},
		{
			name: "more idle than open connections",
			mutate: func(c *config.Config) {
				c.Database.MaxOpenConns = 5
				c.Database.MaxIdleConns = 10
			},
			wantErr: "database.max_idle_conns",
		},
		{
			name:    "negative connection lifetime",
			mutate:  func(c *config.Config) { c.Database.ConnMaxLifetime = -time.Minute },
			wantErr: "database.conn_max_lifetime",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := mutate(tt.mutate).Validate()
			if err == nil {
				t.Fatalf("Validate accepted an invalid %s", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not name %q", err, tt.wantErr)
			}
		})
	}
}

func TestPostgresWithADSNIsValid(t *testing.T) {
	t.Parallel()
	cfg := mutate(func(c *config.Config) {
		c.Database.Driver = config.DriverPostgres
		c.Database.DSN = "postgres://headnet:secret@db:5432/headnet?sslmode=require"
	})
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate rejected a valid postgres configuration: %v", err)
	}
}

func TestNetworkValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cidr    string
		wantErr string
	}{
		{"unparseable", "not-a-cidr", "not a valid CIDR"},
		{"bare address", "100.100.0.0", "not a valid CIDR"},
		{"host bits set", "100.100.0.5/16", "host bits"},
		{"loopback", "127.0.0.0/8", "reserved"},
		{"link local", "169.254.0.0/16", "reserved"},
		{"multicast", "224.0.0.0/8", "reserved"},
		{"too large", "96.0.0.0/4", "prefix length"},
		{"too small", "100.100.0.0/31", "prefix length"},
		{"an IPv6 prefix in the IPv4 field", "fd00::/48", "not an IPv4 prefix"},
		{"empty", "", "must be set"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := mutate(func(c *config.Config) { c.Network.IPv4CIDR = tt.cidr }).Validate()
			if err == nil {
				t.Fatalf("Validate accepted the IPv4 pool %q", tt.cidr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error for %q = %q, want it to mention %q", tt.cidr, err, tt.wantErr)
			}
		})
	}
}

func TestHostBitsErrorSuggestsTheNetworkAddress(t *testing.T) {
	t.Parallel()
	// Silently masking the address would mean the pool an operator reads back
	// is not the one they wrote, so we refuse and show the correct value.
	err := mutate(func(c *config.Config) { c.Network.IPv4CIDR = "100.100.0.5/16" }).Validate()
	if err == nil {
		t.Fatal("Validate accepted a prefix with host bits set")
	}
	if !strings.Contains(err.Error(), "100.100.0.0/16") {
		t.Fatalf("error %q does not suggest the corrected prefix", err)
	}
}

func TestIPv6IsOptional(t *testing.T) {
	t.Parallel()
	cfg := mutate(func(c *config.Config) { c.Network.IPv6CIDR = "" })
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate rejected an IPv4-only deployment: %v", err)
	}
	if _, enabled := cfg.Network.IPv6Pool(); enabled {
		t.Fatal("IPv6Pool reports enabled for an empty prefix")
	}
}

func TestIPv6Validation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cidr    string
		wantErr string
	}{
		{"link local", "fe80::/16", "reserved"},
		{"multicast", "ff00::/16", "reserved"},
		{"an IPv4 prefix in the IPv6 field", "100.100.0.0/16", "not an IPv6 prefix"},
		{"host bits set", "fd7a:115c:a1e0::1/48", "host bits"},
		{"unparseable", "fd00::zz/48", "not a valid CIDR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := mutate(func(c *config.Config) { c.Network.IPv6CIDR = tt.cidr }).Validate()
			if err == nil {
				t.Fatalf("Validate accepted the IPv6 pool %q", tt.cidr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error for %q = %q, want it to mention %q", tt.cidr, err, tt.wantErr)
			}
		})
	}
}

func TestAuthValidation(t *testing.T) {
	t.Parallel()
	if err := mutate(func(c *config.Config) { c.Auth.Providers = nil }).Validate(); err == nil {
		t.Error("Validate accepted a server with no authentication provider")
	}
	if err := mutate(func(c *config.Config) {
		c.Auth.Providers = []config.AuthProvider{"saml"}
	}).Validate(); err == nil {
		t.Error("Validate accepted an unsupported provider")
	}
	if err := mutate(func(c *config.Config) {
		c.Auth.Providers = []config.AuthProvider{config.ProviderLocal, config.ProviderLocal}
	}).Validate(); err == nil {
		t.Error("Validate accepted a duplicated provider")
	}
	if err := mutate(func(c *config.Config) {
		c.Auth.Providers = []config.AuthProvider{config.ProviderLocal, config.ProviderOIDC}
	}).Validate(); err != nil {
		t.Errorf("Validate rejected a valid provider list: %v", err)
	}
}

func TestHasProvider(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	if !cfg.Auth.HasProvider(config.ProviderLocal) {
		t.Error("HasProvider(local) = false for the default configuration")
	}
	if cfg.Auth.HasProvider(config.ProviderOIDC) {
		t.Error("HasProvider(oidc) = true, but OIDC is not enabled by default")
	}
}

func TestRateLimitValidation(t *testing.T) {
	t.Parallel()
	if err := mutate(func(c *config.Config) { c.RateLimit.RequestsPerMinute = 0 }).Validate(); err == nil {
		t.Error("Validate accepted an enabled limiter with a zero rate")
	}
	if err := mutate(func(c *config.Config) { c.RateLimit.Burst = 0 }).Validate(); err == nil {
		t.Error("Validate accepted an enabled limiter with a zero burst")
	}
	// A disabled limiter does not need coherent numbers.
	if err := mutate(func(c *config.Config) {
		c.RateLimit = config.RateLimitConfig{Enabled: false}
	}).Validate(); err != nil {
		t.Errorf("Validate rejected a disabled limiter: %v", err)
	}
}

func TestRateLimitingIsOnByDefault(t *testing.T) {
	t.Parallel()
	// An internet-facing control plane needs a brute-force ceiling before it
	// needs anything else, so this must not regress to opt-in.
	if !config.Default().RateLimit.Enabled {
		t.Fatal("rate limiting is disabled by default")
	}
}

func TestMetricsAreOffByDefault(t *testing.T) {
	t.Parallel()
	// /metrics is unauthenticated, so exposing it has to be a deliberate act.
	if config.Default().Observability.MetricsEnabled {
		t.Fatal("the metrics endpoint is exposed by default")
	}
}

func TestLogValidation(t *testing.T) {
	t.Parallel()
	if err := mutate(func(c *config.Config) { c.Log.Level = "chatty" }).Validate(); err == nil {
		t.Error("Validate accepted an unknown log level")
	}
	if err := mutate(func(c *config.Config) { c.Log.Format = "xml" }).Validate(); err == nil {
		t.Error("Validate accepted an unknown log format")
	}
}

func TestLoadAppliesFileThenEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "headnet.yaml")
	contents := "environment: production\n" +
		"server:\n  base_url: \"https://vpn.example.com\"\n  listen: \":9000\"\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing the fixture failed: %v", err)
	}
	t.Setenv("HEADNET_SERVER_LISTEN", ":9999")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Environment != config.EnvProduction {
		t.Errorf("environment = %q, want production from the file", cfg.Environment)
	}
	if cfg.Server.Listen != ":9999" {
		t.Errorf("server.listen = %q, want the environment to win over the file", cfg.Server.Listen)
	}
	if !cfg.IsProduction() {
		t.Error("IsProduction() = false for a production configuration")
	}
}

func TestLoadRejectsAnInvalidConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "headnet.yaml")
	if err := os.WriteFile(path, []byte("network:\n  ipv4_cidr: \"127.0.0.0/8\"\n"), 0o600); err != nil {
		t.Fatalf("writing the fixture failed: %v", err)
	}
	if _, err := config.Load(path); err == nil {
		t.Fatal("Load returned a configuration that hands out loopback addresses")
	}
}

func TestLoadWithoutAFileUsesDefaults(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Server.Listen != ":8080" {
		t.Fatalf("server.listen = %q, want the default", cfg.Server.Listen)
	}
}

func TestRedactedHidesTheDatabasePassword(t *testing.T) {
	t.Parallel()
	const secret = "postgres://headnet:hunter2@db:5432/headnet"
	cfg := mutate(func(c *config.Config) {
		c.Database.Driver = config.DriverPostgres
		c.Database.DSN = secret
	})

	redacted, err := cfg.Redacted()
	if err != nil {
		t.Fatalf("Redacted failed: %v", err)
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("marshalling failed: %v", err)
	}
	if strings.Contains(string(encoded), "hunter2") {
		t.Fatalf("the redacted configuration leaked the database password:\n%s", encoded)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("the redacted configuration leaked the DSN:\n%s", encoded)
	}
	// Non-secret fields must survive, or the dump is useless for debugging.
	if !strings.Contains(string(encoded), "100.100.0.0/16") {
		t.Fatalf("the redacted configuration dropped a non-secret field:\n%s", encoded)
	}
}
