// Package config defines the Headnet control-plane server configuration: its
// shape, its defaults, and the rules that make a given configuration valid.
//
// Loading mechanics (file, environment, precedence, secret redaction) live in
// packages/config. This package supplies only the schema and the validation,
// because those are server policy rather than a reusable utility.
//
// Validation is deliberately strict and happens once, at start-up, before any
// listener is opened. A control plane that comes up with a subtly wrong
// address pool or a permissive fallback is far worse than one that refuses to
// start with a clear message.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/headnet/headnet/internal/logging"
	pkgconfig "github.com/headnet/headnet/packages/config"
)

// EnvPrefix is prepended to every environment variable, so the server's
// settings cannot collide with anything else on the host.
const EnvPrefix = "HEADNET"

// Environment selects the deployment posture. It is not a feature flag: it
// only tightens or relaxes safety checks and log verbosity.
type Environment string

const (
	// EnvDevelopment relaxes transport checks so the server can be run from a
	// laptop over plain HTTP.
	EnvDevelopment Environment = "development"
	// EnvProduction enforces the stricter checks.
	EnvProduction Environment = "production"
)

// Driver names a supported database backend.
type Driver string

const (
	// DriverSQLite is the single-file default, aimed at the home-server and
	// small-team installations that make up most self-hosted deployments.
	DriverSQLite Driver = "sqlite"
	// DriverPostgres is the recommended backend for larger or
	// highly-available deployments.
	DriverPostgres Driver = "postgres"
)

// AuthProvider names an authentication backend.
type AuthProvider string

const (
	// ProviderLocal is email plus password, stored locally with Argon2id.
	ProviderLocal AuthProvider = "local"
	// ProviderOIDC is any OpenID Connect identity provider.
	ProviderOIDC AuthProvider = "oidc"
)

// Config is the complete server configuration.
type Config struct {
	// Environment is development or production.
	Environment Environment `yaml:"environment" env:"ENVIRONMENT"`

	Server        ServerConfig        `yaml:"server"`
	Database      DatabaseConfig      `yaml:"database"`
	Network       NetworkConfig       `yaml:"network"`
	Auth          AuthConfig          `yaml:"auth"`
	Relay         RelayConfig         `yaml:"relay"`
	Log           LogConfig           `yaml:"log"`
	RateLimit     RateLimitConfig     `yaml:"rate_limit"`
	Observability ObservabilityConfig `yaml:"observability"`
}

// ServerConfig covers the HTTP listener and its timeouts.
type ServerConfig struct {
	// Listen is the address to bind, in host:port form. An empty host binds
	// every interface.
	Listen string `yaml:"listen" env:"SERVER_LISTEN"`
	// BaseURL is the externally reachable URL of this control plane. Clients
	// are enrolled against it and OIDC redirects are built from it, so it has
	// to be the address users actually reach — not the bind address.
	BaseURL string `yaml:"base_url" env:"SERVER_BASE_URL"`
	// AllowInsecureHTTP permits a plain-HTTP BaseURL in production. It exists
	// for the operator who terminates TLS somewhere this server cannot see,
	// and it must be set deliberately.
	AllowInsecureHTTP bool `yaml:"allow_insecure_http" env:"SERVER_ALLOW_INSECURE_HTTP"`
	// ReadTimeout bounds reading a request, headers and body included.
	ReadTimeout time.Duration `yaml:"read_timeout" env:"SERVER_READ_TIMEOUT"`
	// WriteTimeout bounds writing a response.
	WriteTimeout time.Duration `yaml:"write_timeout" env:"SERVER_WRITE_TIMEOUT"`
	// IdleTimeout bounds how long a keep-alive connection may sit unused.
	IdleTimeout time.Duration `yaml:"idle_timeout" env:"SERVER_IDLE_TIMEOUT"`
	// ShutdownTimeout bounds how long a graceful shutdown waits for in-flight
	// requests before the process exits anyway.
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout" env:"SERVER_SHUTDOWN_TIMEOUT"`
	// MaxBodyBytes caps the size of a request body. It is a denial-of-service
	// control, not a tuning knob.
	MaxBodyBytes int64 `yaml:"max_body_bytes" env:"SERVER_MAX_BODY_BYTES"`
}

// DatabaseConfig selects and tunes the storage backend.
type DatabaseConfig struct {
	// Driver is sqlite or postgres.
	Driver Driver `yaml:"driver" env:"DATABASE_DRIVER"`
	// Path is the SQLite database file. Ignored for postgres.
	Path string `yaml:"path" env:"DATABASE_PATH"`
	// DSN is the PostgreSQL connection string. Ignored for sqlite.
	//
	// It normally carries a password, so it is tagged as a secret and is
	// masked everywhere configuration is rendered. Prefer supplying it
	// through the environment rather than the configuration file.
	DSN string `yaml:"dsn" env:"DATABASE_DSN" secret:"true"`
	// MaxOpenConns caps concurrent connections.
	MaxOpenConns int `yaml:"max_open_conns" env:"DATABASE_MAX_OPEN_CONNS"`
	// MaxIdleConns caps pooled idle connections.
	MaxIdleConns int `yaml:"max_idle_conns" env:"DATABASE_MAX_IDLE_CONNS"`
	// ConnMaxLifetime retires a connection after this long, which keeps
	// connections from outliving a failover on the database side.
	ConnMaxLifetime time.Duration `yaml:"conn_max_lifetime" env:"DATABASE_CONN_MAX_LIFETIME"`
}

// NetworkConfig describes the private address space handed out to devices.
type NetworkConfig struct {
	// IPv4CIDR is the pool devices are numbered from. The default sits in the
	// carrier-grade NAT range 100.64.0.0/10, which is the conventional choice
	// for an overlay: it is routable-looking, but almost never present on a
	// LAN, so it rarely collides with a user's home or office network.
	IPv4CIDR string `yaml:"ipv4_cidr" env:"NETWORK_IPV4_CIDR"`
	// IPv6CIDR is the optional IPv6 pool. The default is a unique-local
	// prefix. Empty disables IPv6 addressing.
	IPv6CIDR string `yaml:"ipv6_cidr" env:"NETWORK_IPV6_CIDR"`
}

// AuthConfig selects the enabled authentication backends.
type AuthConfig struct {
	// Providers lists the enabled backends, in the order they are offered.
	Providers []AuthProvider `yaml:"providers" env:"AUTH_PROVIDERS"`
}

// RelayConfig controls the bundled relay coordination. The relay itself is a
// separate component; this only says whether the control plane advertises one.
type RelayConfig struct {
	// Enabled advertises relay availability to clients.
	Enabled bool `yaml:"enabled" env:"RELAY_ENABLED"`
}

// LogConfig configures the structured logger.
type LogConfig struct {
	// Level is debug, info, warn or error.
	Level string `yaml:"level" env:"LOG_LEVEL"`
	// Format is json or text.
	Format string `yaml:"format" env:"LOG_FORMAT"`
	// AddSource records the call site of each record.
	AddSource bool `yaml:"add_source" env:"LOG_ADD_SOURCE"`
}

// RateLimitConfig bounds how fast one client may call the API.
type RateLimitConfig struct {
	// Enabled turns the limiter on. It defaults to on: an unauthenticated
	// control plane on the public internet needs a brute-force ceiling before
	// it needs anything else.
	Enabled bool `yaml:"enabled" env:"RATE_LIMIT_ENABLED"`
	// RequestsPerMinute is the sustained per-client budget.
	RequestsPerMinute int `yaml:"requests_per_minute" env:"RATE_LIMIT_REQUESTS_PER_MINUTE"`
	// Burst is how far above the sustained rate a client may spike.
	Burst int `yaml:"burst" env:"RATE_LIMIT_BURST"`
}

// ObservabilityConfig controls the metrics surface.
type ObservabilityConfig struct {
	// MetricsEnabled exposes /metrics. The endpoint is unauthenticated, so it
	// defaults to off and should be bound to a private interface or placed
	// behind a reverse proxy when turned on.
	MetricsEnabled bool `yaml:"metrics_enabled" env:"OBSERVABILITY_METRICS_ENABLED"`
}

// Default returns a configuration that is safe to run as-is on a developer
// machine, and that requires only a base URL and a database to run in
// production.
func Default() *Config {
	return &Config{
		Environment: EnvDevelopment,
		Server: ServerConfig{
			Listen:          ":8080",
			BaseURL:         "http://localhost:8080",
			ReadTimeout:     15 * time.Second,
			WriteTimeout:    30 * time.Second,
			IdleTimeout:     120 * time.Second,
			ShutdownTimeout: 20 * time.Second,
			MaxBodyBytes:    1 << 20, // 1 MiB
		},
		Database: DatabaseConfig{
			Driver:          DriverSQLite,
			Path:            "./data/headnet.db",
			MaxOpenConns:    25,
			MaxIdleConns:    5,
			ConnMaxLifetime: 30 * time.Minute,
		},
		Network: NetworkConfig{
			IPv4CIDR: "100.100.0.0/16",
			IPv6CIDR: "fd7a:115c:a1e0::/48",
		},
		Auth: AuthConfig{
			Providers: []AuthProvider{ProviderLocal},
		},
		Relay: RelayConfig{Enabled: false},
		Log: LogConfig{
			Level:  "info",
			Format: "json",
		},
		RateLimit: RateLimitConfig{
			Enabled:           true,
			RequestsPerMinute: 120,
			Burst:             30,
		},
		Observability: ObservabilityConfig{MetricsEnabled: false},
	}
}

// Load assembles the configuration from defaults, an optional file and the
// environment, then validates the result.
func Load(path string) (*Config, error) {
	cfg := Default()
	if err := pkgconfig.Load(path, cfg, pkgconfig.Options{EnvPrefix: EnvPrefix}); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Redacted renders the configuration with every secret masked, for start-up
// logging and for `headnet-server -print-config`.
func (c *Config) Redacted() (map[string]any, error) {
	return pkgconfig.Redact(c)
}

// IsProduction reports whether the stricter checks apply.
func (c *Config) IsProduction() bool { return c.Environment == EnvProduction }

// Validate checks the whole configuration and reports every problem it finds
// rather than only the first.
//
// Reporting them together matters: an operator editing a configuration file
// over SSH should not have to restart the server six times to discover six
// typos.
func (c *Config) Validate() error {
	var problems []error
	add := func(err error) {
		if err != nil {
			problems = append(problems, err)
		}
	}

	add(c.validateEnvironment())
	add(c.Server.validate(c.Environment))
	add(c.Database.validate())
	add(c.Network.validate())
	add(c.Auth.validate())
	add(c.Log.validate())
	add(c.RateLimit.validate())

	return errors.Join(problems...)
}

func (c *Config) validateEnvironment() error {
	switch c.Environment {
	case EnvDevelopment, EnvProduction:
		return nil
	case "":
		return errors.New("environment: must be set to development or production")
	default:
		return fmt.Errorf("environment: %q is not valid (expected development or production)", c.Environment)
	}
}

func (s ServerConfig) validate(env Environment) error {
	var problems []error

	if s.Listen == "" {
		problems = append(problems, errors.New("server.listen: must be set, for example \":8080\""))
	} else if _, port, err := net.SplitHostPort(s.Listen); err != nil {
		problems = append(problems, fmt.Errorf("server.listen: %q is not a host:port address: %w", s.Listen, err))
	} else if port == "" {
		problems = append(problems, fmt.Errorf("server.listen: %q does not specify a port", s.Listen))
	}

	switch {
	case s.BaseURL == "":
		problems = append(problems, errors.New("server.base_url: must be set to the URL clients will reach this server on"))
	default:
		u, err := url.Parse(s.BaseURL)
		switch {
		case err != nil:
			problems = append(problems, fmt.Errorf("server.base_url: %q is not a valid URL: %w", s.BaseURL, err))
		case u.Scheme != "http" && u.Scheme != "https":
			problems = append(problems, fmt.Errorf("server.base_url: scheme %q is not supported (expected http or https)", u.Scheme))
		case u.Host == "":
			problems = append(problems, fmt.Errorf("server.base_url: %q has no host", s.BaseURL))
		case env == EnvProduction && u.Scheme == "http" && !s.AllowInsecureHTTP:
			problems = append(problems, errors.New(
				"server.base_url: plain HTTP is refused in production because enrollment "+
					"credentials and session cookies would travel in the clear; use https, "+
					"or set server.allow_insecure_http when TLS is terminated by a proxy in "+
					"front of this server"))
		}
	}

	for _, d := range []struct {
		name  string
		value time.Duration
	}{
		{"server.read_timeout", s.ReadTimeout},
		{"server.write_timeout", s.WriteTimeout},
		{"server.idle_timeout", s.IdleTimeout},
		{"server.shutdown_timeout", s.ShutdownTimeout},
	} {
		if d.value <= 0 {
			problems = append(problems, fmt.Errorf("%s: must be greater than zero, got %s", d.name, d.value))
		}
	}

	if s.MaxBodyBytes <= 0 {
		problems = append(problems, fmt.Errorf("server.max_body_bytes: must be greater than zero, got %d", s.MaxBodyBytes))
	}

	return errors.Join(problems...)
}

func (d DatabaseConfig) validate() error {
	var problems []error

	switch d.Driver {
	case DriverSQLite:
		if strings.TrimSpace(d.Path) == "" {
			problems = append(problems, errors.New("database.path: must be set when the driver is sqlite"))
		}
	case DriverPostgres:
		if strings.TrimSpace(d.DSN) == "" {
			problems = append(problems, errors.New(
				"database.dsn: must be set when the driver is postgres; "+
					"supply it through HEADNET_DATABASE_DSN rather than the configuration file"))
		}
	case "":
		problems = append(problems, errors.New("database.driver: must be set to sqlite or postgres"))
	default:
		problems = append(problems, fmt.Errorf("database.driver: %q is not supported (expected sqlite or postgres)", d.Driver))
	}

	if d.MaxOpenConns <= 0 {
		problems = append(problems, fmt.Errorf("database.max_open_conns: must be greater than zero, got %d", d.MaxOpenConns))
	}
	if d.MaxIdleConns < 0 {
		problems = append(problems, fmt.Errorf("database.max_idle_conns: must not be negative, got %d", d.MaxIdleConns))
	}
	if d.MaxOpenConns > 0 && d.MaxIdleConns > d.MaxOpenConns {
		problems = append(problems, fmt.Errorf(
			"database.max_idle_conns (%d): must not exceed database.max_open_conns (%d)",
			d.MaxIdleConns, d.MaxOpenConns))
	}
	if d.ConnMaxLifetime < 0 {
		problems = append(problems, fmt.Errorf("database.conn_max_lifetime: must not be negative, got %s", d.ConnMaxLifetime))
	}

	return errors.Join(problems...)
}

// reservedIPv4 are ranges an overlay must never hand out. Numbering a device
// from one of them would shadow the host's own loopback or link-local traffic.
var reservedIPv4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
}

var reservedIPv6 = []netip.Prefix{
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

// Bounds on pool size. The lower bound stops an operator from reserving an
// absurd share of the address space; the upper bound guarantees the pool can
// actually hold a useful number of devices.
const (
	minIPv4PrefixBits = 8
	maxIPv4PrefixBits = 30
	minIPv6PrefixBits = 16
	maxIPv6PrefixBits = 120
)

func (n NetworkConfig) validate() error {
	var problems []error

	v4, err := parsePool("network.ipv4_cidr", n.IPv4CIDR, true, minIPv4PrefixBits, maxIPv4PrefixBits, reservedIPv4)
	if err != nil {
		problems = append(problems, err)
	}

	// IPv6 is optional: a deployment may legitimately be IPv4-only.
	var v6 netip.Prefix
	if strings.TrimSpace(n.IPv6CIDR) != "" {
		v6, err = parsePool("network.ipv6_cidr", n.IPv6CIDR, false, minIPv6PrefixBits, maxIPv6PrefixBits, reservedIPv6)
		if err != nil {
			problems = append(problems, err)
		}
	}

	if v4.IsValid() && v6.IsValid() && v4.Overlaps(v6) {
		problems = append(problems, fmt.Errorf(
			"network: the IPv4 pool %s and the IPv6 pool %s overlap", v4, v6))
	}

	return errors.Join(problems...)
}

// parsePool validates one address pool and returns it in canonical form.
func parsePool(field, value string, wantIPv4 bool, minBits, maxBits int, reserved []netip.Prefix) (netip.Prefix, error) {
	if strings.TrimSpace(value) == "" {
		return netip.Prefix{}, fmt.Errorf("%s: must be set, for example \"100.100.0.0/16\"", field)
	}

	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%s: %q is not a valid CIDR block: %w", field, value, err)
	}

	// Reject a mixed-family prefix such as an IPv4-mapped IPv6 address, which
	// parses but behaves surprisingly everywhere downstream.
	if got4 := prefix.Addr().Is4(); got4 != wantIPv4 {
		family := "IPv6"
		if wantIPv4 {
			family = "IPv4"
		}
		return netip.Prefix{}, fmt.Errorf("%s: %q is not an %s prefix", field, value, family)
	}

	// Require the address to be the true network address. Accepting
	// "100.100.0.5/16" and silently masking it would mean the pool an
	// operator reads back is not the one they wrote.
	if masked := prefix.Masked(); masked != prefix {
		return netip.Prefix{}, fmt.Errorf(
			"%s: %q has host bits set; write the network address instead, %q", field, value, masked)
	}

	// Check reserved ranges before prefix length. An operator who typed
	// fe80::/10 needs to hear "that is link-local", not "that prefix is too
	// short" — the first message tells them what is actually wrong.
	for _, r := range reserved {
		if prefix.Overlaps(r) {
			return netip.Prefix{}, fmt.Errorf(
				"%s: %q overlaps the reserved range %s and cannot be used for an overlay network", field, value, r)
		}
	}

	if bits := prefix.Bits(); bits < minBits || bits > maxBits {
		return netip.Prefix{}, fmt.Errorf(
			"%s: prefix length /%d is outside the supported range /%d to /%d", field, bits, minBits, maxBits)
	}

	return prefix, nil
}

func (a AuthConfig) validate() error {
	if len(a.Providers) == 0 {
		return errors.New("auth.providers: at least one provider must be enabled, for example [local]")
	}

	var problems []error
	seen := make(map[AuthProvider]struct{}, len(a.Providers))
	for _, p := range a.Providers {
		switch p {
		case ProviderLocal, ProviderOIDC:
		default:
			problems = append(problems, fmt.Errorf(
				"auth.providers: %q is not supported (expected %s or %s)", p, ProviderLocal, ProviderOIDC))
			continue
		}
		if _, dup := seen[p]; dup {
			problems = append(problems, fmt.Errorf("auth.providers: %q is listed more than once", p))
			continue
		}
		seen[p] = struct{}{}
	}
	return errors.Join(problems...)
}

func (l LogConfig) validate() error {
	var problems []error
	if _, err := logging.ParseLevel(l.Level); err != nil {
		problems = append(problems, fmt.Errorf("log.level: %w", err))
	}
	if _, err := logging.ParseFormat(l.Format); err != nil {
		problems = append(problems, fmt.Errorf("log.format: %w", err))
	}
	return errors.Join(problems...)
}

func (r RateLimitConfig) validate() error {
	if !r.Enabled {
		return nil
	}
	var problems []error
	if r.RequestsPerMinute <= 0 {
		problems = append(problems, fmt.Errorf(
			"rate_limit.requests_per_minute: must be greater than zero when the limiter is enabled, got %d",
			r.RequestsPerMinute))
	}
	if r.Burst <= 0 {
		problems = append(problems, fmt.Errorf(
			"rate_limit.burst: must be greater than zero when the limiter is enabled, got %d", r.Burst))
	}
	return errors.Join(problems...)
}

// IPv4Pool returns the parsed IPv4 pool. It must only be called after Validate
// has succeeded.
func (n NetworkConfig) IPv4Pool() netip.Prefix {
	return netip.MustParsePrefix(n.IPv4CIDR)
}

// IPv6Pool returns the parsed IPv6 pool and whether IPv6 is enabled at all. It
// must only be called after Validate has succeeded.
func (n NetworkConfig) IPv6Pool() (netip.Prefix, bool) {
	if strings.TrimSpace(n.IPv6CIDR) == "" {
		return netip.Prefix{}, false
	}
	return netip.MustParsePrefix(n.IPv6CIDR), true
}

// HasProvider reports whether an authentication provider is enabled.
func (a AuthConfig) HasProvider(p AuthProvider) bool {
	return slices.Contains(a.Providers, p)
}
