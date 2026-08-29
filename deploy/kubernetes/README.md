# Kubernetes

**Status: not provided yet.** Manifests are a Phase 12 item; see
[`docs/ROADMAP.md`](../../docs/ROADMAP.md).

This is deliberate rather than an oversight. Headnet's premise is that a
private network should be deployable on a VPS, a home server, a NAS or a
Raspberry Pi without a cluster, so the supported paths are the simple ones
first:

| Method         | Where it lives                | Status    |
| -------------- | ----------------------------- | --------- |
| Docker         | [`../docker/`](../docker/)    | Supported |
| Docker Compose | [`../compose/`](../compose/)  | Supported |
| systemd        | [`../systemd/`](../systemd/)  | Supported |
| Kubernetes     | here                          | Planned   |

Kubernetes will never be a requirement for running Headnet.

## Running it on Kubernetes today

The container image is an ordinary stateless-looking web service with one
important caveat, so a hand-written Deployment works if you account for it:

- **With SQLite, do not run more than one replica.** The database is a single
  file, and two pods writing to one `ReadWriteMany` volume will corrupt it.
  Use a `StatefulSet` with a single replica and a `ReadWriteOnce` volume, or
  switch to PostgreSQL.
- **Probes:** `/live` for liveness and `/ready` for readiness. Do not point
  liveness at `/ready` — `/ready` fails when the database is briefly
  unreachable, and restarting the control plane over that turns a recoverable
  outage into a crash loop.
- **Secrets:** supply `HEADNET_DATABASE_DSN` from a `Secret`, never from a
  `ConfigMap`.
- **Security context:** the image already runs as UID 65532 with a read-only
  root filesystem; set `readOnlyRootFilesystem: true`,
  `allowPrivilegeEscalation: false` and drop all capabilities.

When manifests do land here they will cover a `StatefulSet`, a `Service`, an
`Ingress` example, a `PodDisruptionBudget` and a Helm chart.
