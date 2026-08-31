# Deployment

Running the Headnet control plane somewhere real.

> **Headnet is pre-release and unaudited, and carries no VPN traffic yet.**
> This guide is written so the operational shape is right from the start, and
> so that anyone experimenting does so safely. Do not use it to protect
> anything that matters.

---

## Before you start

Decide three things. Two of them are expensive to change later.

**1. The address pool.** `network.ipv4_cidr` defines the addresses devices get.
Changing it after enrolment means re-addressing every device. The default,
`100.100.0.0/16`, sits in the carrier-grade NAT range and rarely collides with
a home or office LAN.

**2. The database.** SQLite for a home server, NAS, Pi or small team —
one file, no server to operate. PostgreSQL for larger deployments.

**3. The public URL.** `server.base_url` is what clients are enrolled against.
It should be a name you control and will keep.

---

## Docker Compose (recommended)

The whole installation:

```bash
git clone https://github.com/CauaMora1s/Headnet.git
cd headnet
docker compose -f deploy/compose/docker-compose.yml up -d
curl http://localhost:8080/health
```

The container is published on `127.0.0.1:8080` by default. That is deliberate:
publishing a plain-HTTP control plane straight to `0.0.0.0` would put it on
the internet. Put a reverse proxy in front of it before exposing it.

Configure it with a `.env` file next to the compose file:

```bash
HEADNET_ENVIRONMENT=production
HEADNET_SERVER_BASE_URL=https://vpn.example.com
HEADNET_NETWORK_IPV4_CIDR=100.100.0.0/16
HEADNET_LOG_LEVEL=info
```

### With PostgreSQL

```bash
POSTGRES_PASSWORD=$(openssl rand -base64 32)
cat >> .env <<EOF
POSTGRES_PASSWORD=${POSTGRES_PASSWORD}
HEADNET_DATABASE_DRIVER=postgres
HEADNET_DATABASE_DSN=postgres://headnet:${POSTGRES_PASSWORD}@postgres:5432/headnet?sslmode=disable
EOF

docker compose -f deploy/compose/docker-compose.yml --profile postgres up -d
```

`sslmode=disable` is acceptable only because the connection stays on the
Docker network. Across a network boundary, use `sslmode=require` or stronger.

The compose file has no default for `POSTGRES_PASSWORD` and will refuse to
start without it. A database with a guessable password is worse than one that
fails to start.

---

## Docker

> **No image is published yet.** Headnet has made no release, and CI builds
> the image without pushing it. Build it yourself — do not pull a similarly
> named image from a public registry, because this project does not control
> any such namespace.

```bash
# Build it locally first.
make docker-build

docker run -d \
  --name headnet \
  --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  -v headnet-data:/data \
  -e HEADNET_ENVIRONMENT=production \
  -e HEADNET_SERVER_BASE_URL=https://vpn.example.com \
  --read-only \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  headnet/headnet:latest
```

The image is distroless: no shell, no package manager, no libc, running as
UID 65532. There is very little for an attacker who achieves code execution to
pivot into. `--read-only` works because the only writable path needed is the
`/data` volume.

---

## systemd

For a plain Linux host with no containers.

```bash
# Build
make build

# Install
sudo install -m 0755 bin/headnet-server /usr/local/bin/
sudo install -m 0755 bin/headnet /usr/local/bin/
sudo useradd --system --no-create-home --shell /usr/sbin/nologin headnet
sudo install -d -o headnet -g headnet -m 0750 /etc/headnet
sudo install -m 0644 deploy/systemd/headnet-server.service /etc/systemd/system/

# Configure
sudo cp headnet.example.yaml /etc/headnet/headnet.yaml
sudo nano /etc/headnet/headnet.yaml

# Secrets go in a separate root-owned file the service user cannot read
sudo touch /etc/headnet/headnet.env
sudo chmod 0600 /etc/headnet/headnet.env

sudo systemctl daemon-reload
sudo systemctl enable --now headnet-server
sudo systemctl status headnet-server
journalctl -u headnet-server -f
```

The unit applies substantial hardening: `ProtectSystem=strict`,
`NoNewPrivileges`, `MemoryDenyWriteExecute`, an empty capability bounding set,
a syscall filter, and `RestrictAddressFamilies=AF_INET AF_INET6`. The service
can write to one directory and speak TCP; everything else is taken away.

If you change `server.shutdown_timeout`, raise `TimeoutStopSec` above it, or
systemd will kill the process mid-shutdown.

---

## Reverse proxy and TLS

**Terminate TLS in front of Headnet.** In `production`, the server refuses to
start with a plain-HTTP `base_url` unless you set `allow_insecure_http` — which
is what you do when a proxy handles TLS.

### Caddy

The shortest path to a correct deployment; certificates are automatic.

```caddyfile
vpn.example.com {
    reverse_proxy 127.0.0.1:8080

    # Per-client rate limiting belongs here. See the note below.
}
```

### nginx

```nginx
server {
    listen 443 ssl http2;
    server_name vpn.example.com;

    ssl_certificate     /etc/letsencrypt/live/vpn.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/vpn.example.com/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;

    # Per-client rate limiting. Headnet's own limiter sees only this proxy's
    # address, so this is what actually limits individual clients.
    limit_req_zone $binary_remote_addr zone=headnet:10m rate=120r/m;

    location / {
        limit_req zone=headnet burst=30 nodelay;

        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # For the WebSocket event channel arriving in Phase 3.
        proxy_http_version 1.1;
        proxy_set_header Upgrade    $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 3600s;
    }
}

server {
    listen 80;
    server_name vpn.example.com;
    return 301 https://$host$request_uri;
}
```

The corresponding Headnet configuration:

```yaml
environment: production
server:
  listen: "127.0.0.1:8080"
  base_url: "https://vpn.example.com"
  allow_insecure_http: true    # the proxy terminates TLS
```

### Rate limiting behind a proxy — important

Headnet keys its rate limiter on the transport peer address and **deliberately
ignores `X-Forwarded-For` and `X-Real-IP`**. Those headers are trivially
forged by anyone who can reach the server directly, and honouring them without
a trusted-proxy allowlist would let an attacker evade the limiter entirely by
rotating a header.

The consequence: **behind a proxy, all clients share one bucket.** Headnet's
limiter still protects against a single runaway client, but per-client limits
must be configured at the proxy, as shown above.

Trusted-proxy support is a
[Phase 12](ROADMAP.md#phase-12--production-hardening) item.

---

## Serving the web UI

**The server binary does not serve the web UI yet.** It serves the API only;
embedding the built assets is a Phase 2 item.

Until then, build the UI and serve the static files from the same origin as the
API — the session cookie is `SameSite=Lax` and scoped to that origin, so a UI
on a different host will not be able to sign in.

```bash
make build-web    # writes apps/web/dist
```

With nginx, add a location block alongside the API proxy:

```nginx
    root /var/www/headnet;

    location / {
        try_files $uri /index.html;   # the UI routes client-side
    }

    location /api/ { proxy_pass http://127.0.0.1:8080; }
    location /health { proxy_pass http://127.0.0.1:8080; }
    location /ready  { proxy_pass http://127.0.0.1:8080; }
```

The `try_files` fallback matters: the UI uses real paths rather than hash
routing, so a refresh on `/devices` has to return `index.html` rather than a
404.

For development, `make dev-web` runs Vite with the API proxied, and neither of
these steps is needed.

---

## Health checks

| Endpoint | Use it for | Fails when |
| --- | --- | --- |
| `/live` | Liveness / restart decisions | Never, unless the process is gone |
| `/ready` | Load-balancer rotation | The database is unreachable or unmigrated |
| `/health` | Monitoring and dashboards | Same as `/ready`, with per-dependency detail |

**Do not point a liveness probe at `/ready`.** `/ready` fails when the database
is briefly unreachable, and restarting the control plane over that turns a
recoverable outage into a crash loop.

---

## Backups

**The database is the entire control plane.** There is no other copy.

It contains users, password hashes, device records, public keys, address
allocations and policy. It contains **no private keys**, so a stolen backup
cannot decrypt traffic — but it does reveal the complete shape of your
network, and the password hashes are worth attacking offline. Encrypt backups
and restrict access to them as tightly as the live database.

### SQLite

Use SQLite's own backup command rather than copying the file, which can catch
a write in progress:

```bash
sqlite3 /var/lib/headnet/headnet.db ".backup '/backup/headnet-$(date +%F).db'"
```

Or stop the service, copy, and start it again.

### PostgreSQL

```bash
pg_dump --format=custom --file=/backup/headnet-$(date +%F).dump headnet
```

### Restoring

Stop the server, restore the database, start it again. Migrations run
automatically.

**A restored backup keeps its instance ID**, which matters: clients pin the
instance ID they enrolled against. Restoring the database preserves it and
clients reconnect normally. Starting from an *empty* database generates a
**new** ID, and clients will refuse to reconnect — which is intended, because
it is what distinguishes "my server came back" from "something else is
answering at my server's address".

Test your restore before you need it.

---

## Upgrading

```bash
docker compose -f deploy/compose/docker-compose.yml pull
docker compose -f deploy/compose/docker-compose.yml up -d
```

Migrations run automatically on start. **Back up first** — some migrations are
not reversible, and the notes will say so.

You can migrate separately from serving:

```bash
headnet-server -config /etc/headnet/headnet.yaml -migrate-only
```

Downgrading is refused: a database migrated by a newer build makes the older
binary stop rather than corrupt data. See [upgrading](upgrade.md).

---

## Hardening checklist

- [ ] TLS terminated in front of the control plane
- [ ] `environment: production`
- [ ] `server.listen` bound to loopback when a proxy is in front
- [ ] Per-client rate limiting configured at the proxy
- [ ] The database file readable only by the service user
- [ ] Secrets supplied through the environment, not the YAML file
- [ ] Backups running, encrypted, and **tested by restoring**
- [ ] `/metrics` left off, or bound to a private interface
- [ ] Container running non-root, read-only, with capabilities dropped
- [ ] Host firewall permitting only what is needed
- [ ] Watching [releases](https://github.com/CauaMora1s/Headnet/releases) for
      advisories

---

## Kubernetes

Not provided yet — a [Phase 12](ROADMAP.md#phase-12--production-hardening)
item, and never a requirement. If you want to run it on Kubernetes today, read
[`deploy/kubernetes/README.md`](../deploy/kubernetes/README.md) first; the
SQLite single-replica constraint in particular will corrupt your database if
you ignore it.
