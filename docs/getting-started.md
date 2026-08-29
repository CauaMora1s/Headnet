# Getting started

> **Before you begin.** Headnet does not carry VPN traffic yet. What follows
> genuinely works — the control plane starts, creates and migrates its
> database, and answers its API — but you cannot enrol a device or connect to
> anything. See [What actually works today](../README.md#what-actually-works-today).
>
> If you are looking for a working mesh VPN right now, use
> [Tailscale](https://tailscale.com/), [Headscale](https://github.com/juanfont/headscale)
> or [NetBird](https://netbird.io/). Come back when Phase 3 lands.

---

## What you need

| | |
| --- | --- |
| [Go 1.25+](https://go.dev/dl/) | To build the server and CLI |
| [Node 20+](https://nodejs.org/) and [pnpm](https://pnpm.io/installation) | Only for the web UI |
| [Docker](https://docs.docker.com/get-docker/) | Only for the container route |

No database server is required. SQLite is the default and the file is created
for you.

---

## Option 1: from source

```bash
git clone https://github.com/headnet/headnet.git
cd headnet
make dev
```

On Windows, where `make` is not available:

```powershell
.\scripts\dev.ps1 dev
```

You should see:

```
level=INFO msg="starting the Headnet control plane" version=0.0.0-dev environment=development http=:8080 database=sqlite
level=INFO msg="applied database migration" version=1 name=init
level=INFO msg="control plane listening" address=[::]:8080 base_url=http://localhost:8080 instance_id=net_...
```

The database is created at `./data/headnet.db` on first run.

### Check it works

```bash
curl http://localhost:8080/health
```

```json
{
  "status": "ok",
  "version": "0.0.0-dev",
  "uptime_seconds": 12,
  "checks": [{ "name": "database", "status": "ok", "duration_ms": 0 }]
}
```

Three endpoints, for three different questions:

| | Question it answers |
| --- | --- |
| `/live` | Is the process running? Never fails for a dependency — it is what an orchestrator uses to decide whether to restart the container. |
| `/ready` | Can it serve real traffic? Fails if the database is unreachable or unmigrated. |
| `/health` | Both, with per-dependency detail. |

### What the server speaks

```bash
curl http://localhost:8080/api/v1/version
```

```json
{
  "version": "0.0.0-dev",
  "commit": "unknown",
  "build_date": "unknown",
  "protocol_version": 1,
  "min_protocol_version": 1,
  "capabilities": ["protocol.negotiation"]
}
```

---

## Option 2: Docker

```bash
docker compose -f deploy/compose/docker-compose.yml up -d
curl http://localhost:8080/health
```

The container is bound to `127.0.0.1` by default — publishing a plain-HTTP
control plane straight to the internet would be a poor default. Read
[deployment](deployment.md) before exposing it.

State lives in the `headnet-data` volume. Removing that volume destroys the
control plane.

---

## The web UI

In a second terminal:

```bash
make dev-web
```

Open <http://localhost:5173>. It proxies the API to the Go server on `:8080`,
so both need to be running.

The page shows live control-plane status and an explicit list of what has not
been built. It shows no invented data.

---

## The CLI

```bash
go run ./apps/cli version
go run ./apps/cli diagnostics -server http://localhost:8080
```

```
  ✓  CLI build                 0.0.0-dev (unknown, windows/amd64)
  –  DNS                       the server was given as a literal IP address, so no lookup was needed
  ✓  Control server            localhost:8080 is reachable and reports "ok" (version 0.0.0-dev)
  ✓  Protocol compatibility    agreed on protocol v1 (this build speaks 1–1, the server speaks 1–1)
  –  Client daemon             not built yet (Phase 2)
  –  WireGuard interface       not built yet (Phase 2)
  –  Direct peer connectivity  not built yet (Phase 4)
  –  Relay fallback            not built yet (Phase 5)

Details:
  Client daemon: The background daemon that holds this device's keys and manages WireGuard is not implemented.
  ...
```

A `–` is *skipped*, never *passing*. An unbuilt subsystem is never reported as
healthy — that is the point of the tool.

`-json` gives machine-readable output. Exit codes are documented in
[the CLI reference](user-guide/cli.md).

Commands that are not implemented say so and exit non-zero:

```bash
$ go run ./apps/cli connect
headnet connect: connecting to a network is not implemented yet.

It is planned for Phase 2 of the roadmap:
  https://github.com/headnet/headnet/blob/main/docs/ROADMAP.md
$ echo $?
3
```

---

## Configuring it

Every setting has a default, so no configuration file is needed. To change
something:

```bash
cp headnet.example.yaml headnet.yaml
# edit, then:
go run ./apps/server -config headnet.yaml
```

Or use environment variables, which override the file:

```bash
HEADNET_SERVER_LISTEN=:9090 HEADNET_LOG_LEVEL=debug make dev
```

To see what is actually in effect after defaults, file and environment are
combined:

```bash
go run ./apps/server -config headnet.yaml -print-config
```

Secrets are masked in that output. A populated secret shows as `***`; an unset
one shows as empty, so you can tell "it is set, I cannot see it" from "I
forgot to set it".

Unknown keys are rejected rather than ignored:

```
parsing configuration file headnet.yaml: unknown field "lisen"
```

That is deliberate. A typo in a security-relevant key must not silently leave
a permissive default in place.

Full reference: [configuration](configuration.md).

---

## Using PostgreSQL

```bash
export HEADNET_DATABASE_DRIVER=postgres
export HEADNET_DATABASE_DSN='postgres://headnet:secret@localhost:5432/headnet?sslmode=require'
make dev
```

Migrations run automatically on start. Supply the DSN through the environment
rather than the configuration file — it contains a password.

---

## Running the tests

```bash
make check
```

Or individually:

```bash
make test-go        # Go tests
make test-web       # frontend tests
make lint           # go vet, golangci-lint, eslint, svelte-check
make cover          # coverage report at coverage.html
```

---

## Troubleshooting

**`address already in use`** — something else has port 8080. Use
`HEADNET_SERVER_LISTEN=:9090`.

**`server.base_url: plain HTTP is refused in production`** — you set
`HEADNET_ENVIRONMENT=production` with an `http://` base URL. Use HTTPS, or set
`server.allow_insecure_http` if a proxy terminates TLS in front of Headnet.

**`the database schema is newer than this build understands`** — the database
was migrated by a newer Headnet. Upgrade the binary, or restore a backup taken
before the upgrade. See [upgrading](upgrade.md).

More in [troubleshooting](troubleshooting.md).

---

## Next

- [Architecture](architecture.md) — how the pieces fit together
- [Configuration](configuration.md) — every setting
- [Deployment](deployment.md) — running it somewhere real
- [Roadmap](ROADMAP.md) — what comes next
- [Contributing](../CONTRIBUTING.md) — if you would like to help build it
