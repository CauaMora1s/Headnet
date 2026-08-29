# Troubleshooting

> Headnet does not carry VPN traffic yet, so this covers the control plane,
> configuration and the build. Connectivity troubleshooting arrives with the
> features that create connections.

---

## Start here

```bash
headnet diagnostics -server https://your-server
```

It checks everything this build can check and explains each result, with a
remedy for anything that failed. A `–` means *skipped*, not passing — an
unbuilt subsystem is never reported as healthy.

Then look at what the server itself says:

```bash
curl -s https://your-server/health | jq
journalctl -u headnet-server -n 100        # systemd
docker compose logs -f server               # compose
```

Every error response carries a `request_id`, also in the `X-Request-Id`
header. Grep the server log for it and you will find the matching line.

---

## The server will not start

### `address already in use`

```
binding :8080: listen tcp :8080: bind: address already in use
```

Something else has the port.

```bash
ss -tlnp | grep 8080        # Linux
lsof -i :8080               # macOS
netstat -ano | findstr 8080 # Windows
```

Then either stop it, or use another port:
`HEADNET_SERVER_LISTEN=:9090`.

### `invalid configuration: ...`

Validation runs before anything is bound, and reports **every** problem at
once rather than making you restart per typo:

```
invalid configuration: server.listen: "8080" is not a host:port address
database.driver: "mysql" is not supported (expected sqlite or postgres)
network.ipv4_cidr: "10.0.0.5/8" has host bits set; write the network address instead, "10.0.0.0/8"
```

Each message names the setting. See [configuration](configuration.md).

### `unknown field "..."`

```
parsing configuration file headnet.yaml: unknown field "lisen"
```

A typo. Unknown keys are rejected rather than ignored, deliberately: a typo in
a security-relevant key must not silently leave a permissive default in place.

Compare against [`headnet.example.yaml`](../headnet.example.yaml).

### `configuration file not found`

The `-config` path is wrong. Omit the flag entirely to run on defaults and
environment variables.

### `plain HTTP is refused in production`

```
server.base_url: plain HTTP is refused in production because enrolment
credentials and session cookies would travel in the clear; use https, or set
server.allow_insecure_http when TLS is terminated by a proxy in front of this
server
```

Either use an `https://` base URL, or — if a reverse proxy terminates TLS and
speaks plain HTTP to Headnet on loopback — set
`server.allow_insecure_http: true`. The base URL should still be the `https://`
one your users see.

---

## Database problems

### `the database schema is newer than this build understands`

The database was migrated by a newer Headnet, and running an old binary
against a schema it has never seen would corrupt data.

Upgrade the binary, or restore a backup taken before the upgrade. See
[upgrading](upgrade.md).

### `an already-applied migration has been modified`

```
migration 0001_init.sql no longer matches the version applied on 2026-08-29
(recorded a1b2c3d4e5f6, found 0a1b2c3d4e5f); create a new migration instead of
editing an applied one
```

A migration file changed after it had been applied. This is refused rather
than reconciled, because editing applied migrations is how two deployments of
the same version quietly end up with different schemas.

If you are developing and edited one deliberately: delete the development
database and let migrations recreate it. Never do this to anything real.

### `unable to open database file`

The directory is not writable by the service user. Headnet creates the parent
directory, but cannot create one it lacks permission for.

```bash
sudo install -d -o headnet -g headnet -m 0750 /var/lib/headnet
```

### `connecting to the postgres database` failed

The DSN is deliberately kept out of the error message — it contains a
password. Check it separately:

```bash
psql "$HEADNET_DATABASE_DSN" -c 'select 1'
```

Common causes: wrong host or port, `sslmode` mismatch, the database not
existing, or the user lacking `CREATE` (needed for migrations).

---

## The server runs but something is wrong

### `/health` reports `down`

```json
{ "status": "down", "checks": [{ "name": "database", "status": "down", "detail": "the database is not reachable" }] }
```

The detail is deliberately vague — `/health` is unauthenticated, so anything
it says is public. The server log carries the real error.

### `/health` reports `degraded`

Usually an unmigrated database:

```
the database schema is not at the version this build expects; run the server once to migrate
```

Migrations run automatically at start-up. If they did not, the log says why.

### `/metrics` returns 501

Expected. Metrics are enabled in your configuration but Prometheus
instrumentation is a [Phase 12](ROADMAP.md#phase-12--production-hardening)
item. It returns `501` rather than an empty metrics page, because a dashboard
would render an empty page as a healthy, entirely fictional zero.

Set `observability.metrics_enabled: false` and the endpoint returns `404`.

### Everything returns 429

Rate limiting. The default is 120 requests per minute per client with a burst
of 30.

If this happens **behind a reverse proxy**, that is the known limitation:
Headnet sees only the proxy's address, so all clients share one bucket.
Forwarding headers are deliberately not trusted, because honouring them would
let an attacker reset the bucket by rotating a header.

Raise `rate_limit.requests_per_minute`, and configure per-client limits at
your proxy. See [deployment](deployment.md).

### A `404` from an endpoint you expected

Devices, users, routes and policies do not exist yet. They return `404` rather
than an empty list, because an empty list would read as "you have no devices"
rather than "this has not been built".

[`docs/api/`](api/) lists what exists today.

---

## The web UI

### "The control server could not be reached"

The Go server is not running, or the dev proxy is pointed at the wrong place.
Both need to be up:

```bash
make dev        # terminal 1, :8080
make dev-web    # terminal 2, :5173
```

If your server is elsewhere:

```bash
HEADNET_DEV_SERVER=http://127.0.0.1:9090 pnpm --filter @headnet/web dev
```

### Blank page

Check the browser console. If the build succeeded but nothing renders, the
`#app` mount point is missing from `index.html`.

---

## The CLI

### Exit code 3

The feature is not implemented in this build. The message names the roadmap
phase. This is not a failure to diagnose — it is the honest answer.

### Exit code 4

A dependency was unreachable — the daemon or the control server.

### `diagnostics` shows all-skipped checks

Expected without `-server`. Pass a control server URL to test reachability and
protocol compatibility.

---

## Building

### `cgo: C compiler "gcc" not found`

Only `-race` needs a C compiler; everything else builds with `CGO_ENABLED=0`.
CI runs the race detector on Linux for every change, so a race is caught even
if you cannot run it locally.

To run it locally, install a C toolchain — build-essential, Xcode command line
tools, or TDM-GCC/MSYS2 on Windows.

### `go: cannot find module`

```bash
go mod download
```

### pnpm reports an ignored build script for esbuild

The root `package.json` carries an `onlyBuiltDependencies` allowlist. Run
`pnpm install` again, or `pnpm rebuild esbuild`.

### `golangci-lint: command not found`

Optional. `make lint` skips it with a note. Install it for the full suite.

---

## Reporting a problem

Open an issue with:

1. `headnet version`
2. `headnet diagnostics -server <url> -json`
3. The relevant server log lines — including the `request_id` if you have one
4. What you expected, and what happened

Redact hostnames, addresses and anything that looks like a credential first.

**If it is a security problem, do not open an issue.** See
[SECURITY.md](../SECURITY.md).
