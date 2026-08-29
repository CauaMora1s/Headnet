# Internal architecture

The package layout, the rules between layers, and where to put new code.

For the system-level view, see [../architecture.md](../architecture.md).

---

## Three layers

```
apps/          programs — wiring only, no business logic
   ↓ imports
internal/      control-plane implementation          (AGPL-3.0)
   ↓ imports
packages/      shared contracts                      (Apache-2.0)
```

**Dependencies point downwards only.** The rules:

| Rule | Enforced by |
| --- | --- |
| `packages/` must not import `internal/` | Review, and the Go toolchain for external consumers |
| `packages/` must not take a copyleft dependency | CI dependency review |
| `apps/` contains wiring, not logic | Review |

The `packages/` rules exist because that layer is Apache-2.0 and is meant to
be reusable by anyone writing an alternative client. See
[ADR-0009](../architecture/decisions/ADR-0009-licensing.md).

---

## `packages/` — the contracts

Apache-2.0. Small, dependency-light, and free of business logic. If a third
party cannot write a client without reading `internal/`, something belongs
here that is not here yet.

| | |
| --- | --- |
| `protocol` | Version and capability negotiation |
| `api` | HTTP DTOs and the error contract |
| `config` | Layered configuration loading and secret redaction |
| `shared` | Small dependency-free helpers: IDs, clock |

New files here carry an Apache-2.0 SPDX header.

### `protocol`

The rules for talking across a version gap. One monotonically increasing
integer, plus named capabilities for optional behaviour. Adding a capability
is always backwards compatible; unknown capabilities are ignored rather than
rejected, so an old peer talking to a new one degrades instead of failing.

### `api`

`ErrorCode` is the stable contract clients branch on. A code may be added but
never renamed or repurposed. `HTTPStatus()` maps each one to its status in a
single place, which is what guarantees a given code always arrives with the
same status.

### `config`

Layered loading — defaults, file, environment — with strict YAML that rejects
unknown keys, and `Redact` for masking anything tagged `secret:"true"`.

It supplies the *mechanics*. The server's actual schema lives in
`internal/config`, because that is policy, not a reusable utility.

---

## `internal/` — the implementation

AGPL-3.0. The Go toolchain makes this unimportable from outside the module,
which is what lets it change freely.

### Built

| | |
| --- | --- |
| `config` | The server's configuration schema and validation |
| `logging` | `log/slog` setup, request-ID propagation |
| `storage` | Database connections, migrations, metadata |
| `httpapi` | Middleware and response helpers |
| `server` | Routing and process lifecycle |
| `cliutil` | The CLI command tree |
| `version` | Build identity |

### Placeholders

`auth`, `devices`, `network`, `wireguard`, `signaling`, `relay`, `acl`, `dns`,
`routes` — each contains a note describing what belongs there and which
roadmap phase delivers it. No code.

---

## `apps/` — the programs

Each directory is a binary. They wire dependencies together and own no
business logic, so that anything worth testing lives in a package a test can
reach without starting a process.

`apps/server/main.go` is the model: parse flags, load configuration, build the
logger, open and migrate the database, construct the server, run it. Its body
is `run(argv, stdout, stderr) error`, so `main` does nothing but call it and
choose an exit code.

---

## Conventions

### Errors

Wrap with context, and name the thing that failed:

```go
return fmt.Errorf("binding %s: %w", cfg.Server.Listen, err)
```

Sentinel errors for conditions a caller must branch on:

```go
var ErrChecksumMismatch = errors.New("an already-applied migration has been modified")
```

Test them with `errors.Is`/`errors.As`, never by matching strings.

Aggregate independent failures with `errors.Join` so a user sees every problem
at once — that is why configuration validation reports six typos in one run
rather than six restarts.

### Context

Every blocking call takes a `context.Context`, first parameter.

One exception worth knowing: graceful shutdown builds its deadline from
`context.Background()`, because the shutdown timeout must survive the
cancellation of the context that triggered it.

### Logging

`slog`, through `internal/logging`. Request IDs travel in the context and are
attached automatically, so a handler cannot forget one.

```go
logger.InfoContext(ctx, "applied database migration", "version", m.Version, "name", m.Name)
```

Never log a secret. Never log a query string.

### Options structs

Constructors take a struct rather than a long parameter list, and validate it:

```go
func New(opts Options) (*Server, error) {
	if opts.Config == nil {
		return nil, errors.New("server: a configuration is required")
	}
	...
}
```

`server.New` also re-validates the configuration rather than trusting the
caller — a server handed an invalid configuration must not open a listener.

### Injecting time

Anything with expiry or duration logic takes a `shared.Clock`, so behaviour is
tested deterministically rather than with sleeps.

### Testability

Streams, clocks, `Getenv` and `fetch` are injected. `main` bodies are
extracted into a testable `run`. Handlers are plain `http.HandlerFunc` so
`httptest` is enough.

---

## Where does new code go?

```
Is it part of the contract a third-party client needs?
  → packages/

Is it control-plane business logic?
  → internal/<domain>/

Is it wiring for a program?
  → apps/<program>/

Is it a helper with no Headnet-specific knowledge?
  → packages/shared/
```

When unsure, prefer `internal/`. Moving something out to `packages/` later is
easy; taking it back is a breaking change for anyone who started using it.

---

## Middleware order

Set in `internal/server.buildHandler`, and security-relevant:

```
Recoverer          outermost, so it catches a panic anywhere inside
RequestID          before the logger, so every line has an ID
WithLogger
RequestLogger
SecurityHeaders
RateLimit          before routing and before auth
MaxBodyBytes
→ ServeMux
```

Two of these are load-bearing:

- **Recoverer outermost.** A panic in another middleware must still produce a
  500 rather than killing the connection.
- **RateLimit before routing and authentication.** Login, enrolment and
  protocol negotiation are necessarily unauthenticated; a limiter that only
  ran after authentication would protect nothing that matters.

Changing this order requires understanding why each position is where it is.
The reasoning is in the code, at the point where the chain is built.

---

## Adding an endpoint

1. Add the DTOs to `packages/api`.
2. Add the handler to `internal/server`, using `httpapi.WriteJSON` /
   `httpapi.WriteError`.
3. Register it in `routes()`.
4. Test it: success, every failure, and the authorisation boundary.
5. Update [`docs/api/openapi.yaml`](../api/openapi.yaml).

If the behaviour is not built yet, use `httpapi.NotImplemented` with the
roadmap phase — never an empty success.

## Adding a migration

1. Create `NNNN_description.sql` in **both** `migrations/sqlite/` and
   `migrations/postgres/`.
2. Never edit an applied migration; the checksum ledger will refuse it.
3. Run the tests — one fails if the two dialects disagree.
4. If it cannot be safely reversed, say so in the file and in
   [`docs/upgrade.md`](../upgrade.md).
