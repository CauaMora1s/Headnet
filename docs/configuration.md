# Configuration

Every setting the control plane accepts, what it does, and what happens if you
get it wrong.

Templates: [`headnet.example.yaml`](../headnet.example.yaml) and
[`.env.example`](../.env.example).

---

## How settings are resolved

Lowest priority to highest:

1. **Defaults** compiled into the binary. Every setting has one, so an empty
   configuration is a valid configuration.
2. **A YAML file**, if you pass `-config`.
3. **Environment variables**, which always win over the file.

```bash
headnet-server -config /etc/headnet/headnet.yaml
HEADNET_SERVER_LISTEN=:9090 headnet-server
```

Every setting has an environment variable: uppercase the YAML path, replace
dots with underscores, and prefix with `HEADNET_`. So `server.base_url` becomes
`HEADNET_SERVER_BASE_URL`.

### Seeing what is actually in effect

```bash
headnet-server -config /etc/headnet/headnet.yaml -print-config
```

This is the answer to "why is it not picking up my setting?" — it prints the
result of all three layers combined, with secrets masked.

### Two behaviours worth knowing

**Unknown keys are rejected.**

```
parsing configuration file headnet.yaml: unknown field "lisen"
```

A typo in a security-relevant key must not silently leave a permissive default
in place. This is why the loader is strict.

**An empty environment variable means "not set".** Exporting
`HEADNET_SERVER_LISTEN=""` leaves the default in place rather than blanking
the listen address, because an empty exported variable is nearly always an
accident.

**Validation reports everything at once.** An operator editing a file over SSH
should not have to restart the server once per typo:

```
invalid configuration: server.listen: "8080" is not a host:port address
database.driver: "mysql" is not supported (expected sqlite or postgres)
network.ipv4_cidr: "10.0.0.5/8" has host bits set; write the network address instead, "10.0.0.0/8"
```

---

## `environment`

| | |
| --- | --- |
| **Type** | `development` or `production` |
| **Default** | `development` |
| **Env** | `HEADNET_ENVIRONMENT` |

Not a feature flag. It only tightens safety checks: in `production`, a
plain-HTTP `base_url` is refused unless you explicitly opt out.

---

## `server`

### `server.listen`

| | |
| --- | --- |
| **Default** | `:8080` |
| **Env** | `HEADNET_SERVER_LISTEN` |

The bind address, in `host:port` form. An empty host binds every interface.
Use `127.0.0.1:8080` to bind only loopback when a reverse proxy is in front.

### `server.base_url`

| | |
| --- | --- |
| **Default** | `http://localhost:8080` |
| **Env** | `HEADNET_SERVER_BASE_URL` |

**The URL your users and devices actually reach.** Not the bind address —
enrolment and OIDC redirects are built from this, so if it is wrong, clients
will be sent somewhere that does not exist.

In `production` it must be `https://` unless `allow_insecure_http` is set. It
also controls whether HSTS is sent: only an HTTPS base URL enables it, because
sending HSTS from a plain-HTTP development server would pin a developer's
browser to HTTPS on localhost.

### `server.allow_insecure_http`

| | |
| --- | --- |
| **Default** | `false` |
| **Env** | `HEADNET_SERVER_ALLOW_INSECURE_HTTP` |

Permits a plain-HTTP `base_url` in production.

Set this **only** when TLS is terminated by a reverse proxy Headnet cannot
see. Without TLS somewhere in the path, enrolment credentials and session
cookies travel in the clear.

### Timeouts

| Setting | Default | Purpose |
| --- | --- | --- |
| `server.read_timeout` | `15s` | Reading a request, headers and body |
| `server.write_timeout` | `30s` | Writing a response |
| `server.idle_timeout` | `120s` | How long a keep-alive connection may sit unused |
| `server.shutdown_timeout` | `20s` | How long a graceful shutdown waits for in-flight requests |

All must be greater than zero.

There is also a fixed 10-second header-read timeout, not configurable, so a
slowloris client cannot hold a connection for the full read window.

If you run under systemd, `TimeoutStopSec` must exceed `shutdown_timeout`, or
systemd will kill the process mid-shutdown.

### `server.max_body_bytes`

| | |
| --- | --- |
| **Default** | `1048576` (1 MiB) |
| **Env** | `HEADNET_SERVER_MAX_BODY_BYTES` |

A denial-of-service control, not a tuning knob. Without it, one client can
make the server buffer as much memory as it likes. Exceeding it returns `413`.

---

## `database`

### `database.driver`

| | |
| --- | --- |
| **Type** | `sqlite` or `postgres` |
| **Default** | `sqlite` |
| **Env** | `HEADNET_DATABASE_DRIVER` |

SQLite is the right choice for a home server, a NAS or a small team — the
whole control plane is one file you can copy as a backup. PostgreSQL is for
larger deployments and for anyone who wants replication.

### `database.path`

| | |
| --- | --- |
| **Default** | `./data/headnet.db` |
| **Env** | `HEADNET_DATABASE_PATH` |

SQLite only. The parent directory is created if it does not exist.

**This file is the entire control plane.** Back it up.

### `database.dsn`

| | |
| --- | --- |
| **Default** | *(empty)* |
| **Env** | `HEADNET_DATABASE_DSN` |
| **Secret** | Yes |

PostgreSQL only. Required when the driver is `postgres`.

```
postgres://headnet:password@localhost:5432/headnet?sslmode=require
```

**Supply this through the environment, not the YAML file.** It contains a
password. It is masked wherever Headnet renders its configuration, and never
appears in an error message.

### Connection pool

| Setting | Default | Notes |
| --- | --- | --- |
| `database.max_open_conns` | `25` | **Ignored for SQLite**, which is held to one connection |
| `database.max_idle_conns` | `5` | Must not exceed `max_open_conns` |
| `database.conn_max_lifetime` | `30m` | Keeps connections from outliving a database failover |

SQLite serialises writers, so the pool is fixed at one connection. That
removes "database is locked" failures entirely at the cost of serialising
reads. See
[ADR-0005](architecture/decisions/ADR-0005-database-sqlite-postgres.md).

---

## `network`

> These settings define the address space your devices are numbered from.
> **Changing them after devices are enrolled requires re-addressing every
> one.** Choose them before you roll out.

### `network.ipv4_cidr`

| | |
| --- | --- |
| **Default** | `100.100.0.0/16` |
| **Env** | `HEADNET_NETWORK_IPV4_CIDR` |

The default sits inside `100.64.0.0/10`, the carrier-grade NAT range. That is
the conventional choice for an overlay because it is almost never present on a
home or office LAN, so it rarely collides with networks your users are already
on. Using `192.168.x.x` would collide with most home routers.

Validation refuses:

- Anything overlapping loopback, link-local, multicast or reserved space
- A prefix with host bits set — `100.100.0.5/16` is rejected with the corrected
  value in the message, because silently masking it would mean the pool you
  read back is not the one you wrote
- A prefix outside `/8` to `/30`

### `network.ipv6_cidr`

| | |
| --- | --- |
| **Default** | `fd7a:115c:a1e0::/48` |
| **Env** | `HEADNET_NETWORK_IPV6_CIDR` |

Optional; set it empty for an IPv4-only network. The default is a unique-local
prefix. Must be `/16` to `/120`, and must not overlap the IPv4 pool.

---

## `auth`

### `auth.providers`

| | |
| --- | --- |
| **Default** | `[local]` |
| **Env** | `HEADNET_AUTH_PROVIDERS` (comma-separated) |

Supported: `local`, `oidc`. At least one is required, and duplicates are
rejected.

> **Not implemented yet** ([Phase 1](ROADMAP.md#phase-1--server-mvp)). The
> setting is validated but nothing acts on it.

---

## `log`

| Setting | Default | Values |
| --- | --- | --- |
| `log.level` | `info` | `debug`, `info`, `warn`, `error` |
| `log.format` | `json` | `json`, `text` |
| `log.add_source` | `false` | Records the file and line of each call |

Use `json` in production so a log shipper can parse it, `text` when reading in
a terminal.

Logs go to **stderr**, so stdout stays free for anything a command genuinely
outputs.

Query strings are never logged, because OIDC callbacks and enrolment flows
carry codes in them.

---

## `rate_limit`

| Setting | Default | |
| --- | --- | --- |
| `rate_limit.enabled` | `true` | |
| `rate_limit.requests_per_minute` | `120` | Sustained per-client budget |
| `rate_limit.burst` | `30` | How far above the sustained rate a client may spike |

On by default, and applied **before routing and before authentication** —
login, enrolment and protocol negotiation are necessarily unauthenticated, and
they are exactly what needs a ceiling.

Exceeding the budget returns `429` with a `Retry-After` header.

> **Behind a reverse proxy, every request appears to come from the proxy**, so
> all clients share one bucket. Forwarding headers are deliberately not
> trusted: honouring them would let an attacker reset the bucket by rotating a
> header. Configure per-client limits at your proxy until trusted-proxy
> support lands in Phase 12. See [deployment](deployment.md).

---

## `observability`

### `observability.metrics_enabled`

| | |
| --- | --- |
| **Default** | `false` |
| **Env** | `HEADNET_OBSERVABILITY_METRICS_ENABLED` |

Exposes `/metrics`. The endpoint is unauthenticated, so bind it to a private
interface or put it behind your proxy before enabling it.

> **Not implemented yet** ([Phase 12](ROADMAP.md#phase-12--production-hardening)).
> With this enabled, `/metrics` returns `501`. It does **not** return an empty
> metrics page, because a dashboard would render that as a healthy zero.

---

## `relay`

### `relay.enabled`

| | |
| --- | --- |
| **Default** | `false` |
| **Env** | `HEADNET_RELAY_ENABLED` |

Advertises a relay to clients.

> **Not implemented yet** ([Phase 5](ROADMAP.md#phase-5--relay)).

---

## Complete example

Production, behind a reverse proxy, with PostgreSQL:

```yaml
# /etc/headnet/headnet.yaml
environment: production

server:
  listen: "127.0.0.1:8080"          # loopback only; the proxy reaches it
  base_url: "https://vpn.example.com"
  allow_insecure_http: true          # the proxy terminates TLS
  shutdown_timeout: 20s

database:
  driver: postgres
  # dsn comes from the environment

network:
  ipv4_cidr: "100.100.0.0/16"
  ipv6_cidr: "fd7a:115c:a1e0::/48"

auth:
  providers: [local]

log:
  level: info
  format: json

rate_limit:
  enabled: true
  requests_per_minute: 120
  burst: 30
```

```bash
# /etc/headnet/headnet.env — mode 0600, owned by root
HEADNET_DATABASE_DSN=postgres://headnet:REAL_PASSWORD@db.internal:5432/headnet?sslmode=require
```

Note `allow_insecure_http: true` alongside an `https://` base URL: the base
URL is what clients see, while the proxy speaks plain HTTP to Headnet on
loopback.

---

## Secrets

Only `database.dsn` currently holds a secret; OIDC client secrets join it in
Phase 1.

- Supply them through the environment, or a root-owned `EnvironmentFile`.
- They are masked wherever configuration is rendered.
- A populated secret shows as `***`; an unset one shows as empty, so you can
  tell "it is set" from "I forgot it".
- Never commit a filled-in `.env`. It is in `.gitignore`, and CI scans history
  for leaked credentials.
