# Upgrading

How Headnet versions itself, what happens to the database, and how to get back
if an upgrade goes wrong.

> **No release has been made.** `main` is the only tree, and its schema may
> change without a migration path until the first tagged release. Everything
> below describes the process from that point on — and the mechanisms
> themselves (the migration ledger, the downgrade refusal) are already
> implemented and tested.

---

## Three versions, three meanings

| | Changes when | Compatibility |
| --- | --- | --- |
| **Release version** | Every release | Semantic versioning |
| **Protocol version** | A breaking client/server change | Negotiated per connection |
| **Schema version** | Any migration | Forward only |

They move independently. Most releases change only the first.

---

## Upgrade order

**Upgrade the server first, then the clients.**

The server is built to accept older clients within its supported protocol
range. The reverse — a new client against an old server — is only guaranteed
when their ranges overlap, and a client cannot make an old server understand a
new protocol.

```
1. Back up the database
2. Upgrade the server        (migrations run at start-up)
3. Verify /health and /ready
4. Upgrade clients, at whatever pace suits you
```

Clients do not need to be upgraded together, which is the entire reason
protocol negotiation exists.

---

## Server upgrade

### Docker Compose

```bash
docker compose -f deploy/compose/docker-compose.yml pull
docker compose -f deploy/compose/docker-compose.yml up -d
docker compose -f deploy/compose/docker-compose.yml logs -f server
```

### systemd

```bash
sudo systemctl stop headnet-server
sudo install -m 0755 bin/headnet-server /usr/local/bin/
sudo systemctl start headnet-server
sudo systemctl status headnet-server
```

### Verify

```bash
curl -s http://localhost:8080/health | jq
curl -s http://localhost:8080/api/v1/version | jq
```

`status` should be `ok`, and `version` should be the release you installed.

---

## Migrations

Migrations are embedded in the binary, versioned, checksummed, and applied
automatically at start-up. There is nothing to run by hand.

To migrate without serving — useful when you want the schema change and the
traffic switch to be separate steps:

```bash
headnet-server -config /etc/headnet/headnet.yaml -migrate-only
```

Each migration runs inside a transaction together with its ledger entry, so a
process killed midway either applied the change and recorded it, or did
neither.

### Three refusals worth understanding

**A modified migration is refused.**

```
an already-applied migration has been modified: migration 0001_init.sql no
longer matches the version applied on 2026-08-29; create a new migration
instead of editing an applied one
```

The ledger records a checksum. Editing an applied migration is how two
deployments of the same version quietly end up with different schemas, so it
is refused rather than reconciled.

**A newer schema is refused.**

```
the database schema is newer than this build understands: migration 7
(add_policies) was applied on 2026-09-15 but is not present in this build;
upgrade the server, or restore the database from a backup taken before the
upgrade
```

This is the downgrade guard. Running an old binary against a schema it has
never seen would corrupt data, so start-up stops instead.

**Migrations are forward-only.** There are no down migrations, deliberately: a
down migration that has to discard data is not a rollback, it is a second
destructive operation, and having one available encourages using it under
pressure. **Rolling back means restoring a backup.**

Any migration that cannot be reversed by restoring a backup says so in the
file and in the release notes.

---

## Rolling back

```bash
# 1. Stop the server
sudo systemctl stop headnet-server

# 2. Restore the database from before the upgrade
sudo -u headnet sqlite3 /var/lib/headnet/headnet.db ".restore '/backup/headnet-2026-08-29.db'"
#   or, for PostgreSQL:
#   pg_restore --clean --dbname=headnet /backup/headnet-2026-08-29.dump

# 3. Reinstall the previous binary
sudo install -m 0755 /path/to/previous/headnet-server /usr/local/bin/

# 4. Start
sudo systemctl start headnet-server
```

Restoring the database file **preserves the instance ID**, so clients
reconnect normally. Starting from an *empty* database generates a new one and
clients will refuse to reconnect — see below.

Anything that happened between the backup and the rollback is lost. Devices
enrolled in that window will have to enrol again.

---

## Protocol compatibility

The client and server each advertise the newest version they speak and the
oldest they accept. They are compatible when the ranges overlap.

```
Server:  speaks 1..3
Client:  speaks 1..2      →  agreed: v2
Client:  speaks 4..5      →  incompatible; upgrade the server
Client:  speaks 1..1      →  agreed: v1
```

A mismatch produces `426 Upgrade Required` with a message naming which side is
stale:

```json
{
  "error": {
    "code": "unsupported_protocol_version",
    "message": "incompatible protocol version: the peer is too old (local supports 2..3, peer supports 1..1); upgrade the peer"
  }
}
```

Optional behaviour is gated on **named capabilities**, never on a version
comparison, so adding a feature is always backwards compatible and an old peer
talking to a new one degrades rather than fails. See
[protocol versioning](architecture/protocol-versioning.md).

To check compatibility before upgrading anything:

```bash
headnet diagnostics -server https://vpn.example.com
```

---

## The instance ID

Each control plane generates a stable identity on first start and keeps it in
the database.

Clients record the instance they enrolled against. If it changes, the client
is talking to a **different network at the same address** and must refuse to
continue rather than silently trusting whatever is answering.

| | Instance ID |
| --- | --- |
| Upgrade | Unchanged |
| Restore a backup | Unchanged |
| Rebuild from an empty database | **New** — clients refuse to reconnect |

That refusal is the intended behaviour, not a bug: it is the signal that
distinguishes "my server came back" from "something else is answering at my
server's address". If you genuinely rebuilt the control plane, devices must
enrol again.

---

## Before any upgrade

- [ ] Read the release notes, particularly anything marked breaking
- [ ] **Back up the database, and know how to restore it**
- [ ] Keep the previous binary or image tag available
- [ ] Check protocol compatibility if clients will lag behind
- [ ] Upgrade a staging deployment first, if you have one
- [ ] Have a rollback plan you have actually tested

---

## Versioning policy

Semantic versioning from the first release.

| | Means |
| --- | --- |
| **Major** | A breaking change. Migration notes in the release. |
| **Minor** | New features, backwards compatible. |
| **Patch** | Fixes only. |

Before 1.0, minor versions may contain breaking changes; the release notes
will say so plainly.

Security fixes are released for the current minor version and, once there are
tagged releases, are accompanied by an advisory. See
[SECURITY.md](../SECURITY.md).
