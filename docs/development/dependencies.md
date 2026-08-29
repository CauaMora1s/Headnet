# Dependencies

Headnet has **three** direct Go dependencies. That is deliberate: everything
on the path of a request is code this project is responsible for.

---

## The policy

Before adding a dependency, answer these — in the pull request, not just in
your head:

1. **Is the standard library genuinely insufficient?** Go 1.22+ `net/http`
   routing removed the usual reason for a router. `log/slog` removed the usual
   reason for a logging library. `database/sql` covers most of what an ORM
   would.
2. **Is it actively maintained?** Recent commits, responsive issues, a real
   release history.
3. **Is the licence compatible?** Permissive only. Copyleft cannot go in
   `packages/` — see [licensing](../licensing.md).
4. **Does it have known vulnerabilities?** `govulncheck`, and its advisory
   history.
5. **What does it pull in transitively?** A small library with thirty
   transitive dependencies is not a small library.
6. **Does it create vendor lock-in?**

A dependency that is smaller than the code it replaces is usually not worth
it. So is one that saves fifty lines but sits on the request path.

---

## Direct Go dependencies

### `modernc.org/sqlite` — BSD-3-Clause

A pure-Go translation of SQLite.

**Why not the standard choice.** `mattn/go-sqlite3` is faster and far more
widely used, but requires cgo. Headnet builds with `CGO_ENABLED=0` so that one
CI job produces static binaries for seven platforms, and so the container
image can be distroless with no libc. The performance difference is irrelevant
at control-plane query volumes; the build simplicity is not.

**Cost.** It is a large dependency — it carries a translated libc — and slower
than the cgo binding. Both accepted for the build properties. See
[ADR-0005](../architecture/decisions/ADR-0005-database-sqlite-postgres.md).

### `github.com/jackc/pgx/v5` — MIT

The de facto PostgreSQL driver for Go. Used through its `database/sql`
adapter, so repository code stays backend-agnostic.

**Why not `lib/pq`.** In maintenance mode; pgx is the actively developed
successor.

### `github.com/goccy/go-yaml` — MIT

YAML parsing for configuration files.

**Why a dependency at all.** The standard library has no YAML parser, and YAML
is what operators expect for a configuration file.

**Why not `gopkg.in/yaml.v3`.** The historical default, now effectively
unmaintained. For a security-focused project, an unmaintained parser handling
operator-supplied input is not a good position.

**Why this is low-risk despite the churn in the YAML ecosystem.** It is used
only behind `packages/config`, so replacing it is a one-file change. That
containment is the actual mitigation.

---

## Deliberately not used

Each of these was considered and rejected. The reasoning is recorded so it is
not re-litigated every six months.

| | Instead | Why |
| --- | --- | --- |
| **chi / gorilla / gin** | `net/http.ServeMux` | Go 1.22+ has method-and-pattern routing. Nothing on the request path. [ADR-0010](../architecture/decisions/ADR-0010-standard-library-http-router.md) |
| **cobra / urfave-cli** | `flag` + a 250-line command tree | Two levels of nesting does not justify a framework. [ADR-0011](../architecture/decisions/ADR-0011-cli-standard-library.md) |
| **zap / logrus** | `log/slog` | Structured logging is in the standard library now |
| **viper** | `packages/config` | We need file + env + validation, not twelve sources and magic |
| **golang-migrate** | `internal/storage` | ~150 lines, embedded, checksummed, and we control the failure behaviour |
| **testify** | `testing` | Table-driven tests with real messages read better than assertion DSLs |
| **GORM / sqlc / ent** | `database/sql` | Simple queries; an ORM obscures what reaches the database, and leaks exactly where the two dialects differ |
| **prometheus/client_golang** | *(not yet)* | Phase 12. Adding it now would mean an unused dependency |

The migration runner is worth a note. Writing one is normally a bad idea, but
the requirements here are unusual and specific: embedded files, two dialects,
checksum verification of applied migrations, and a hard refusal to run against
a newer schema. It is ~150 lines and fully tested, and we control what happens
when something is wrong — which is the part that matters.

---

## Frontend

Build-time only. Nothing here ships in the server binary or reaches an end
user's browser as a dependency.

| | |
| --- | --- |
| `svelte` | UI framework |
| `vite`, `@sveltejs/vite-plugin-svelte` | Build tooling |
| `tailwindcss`, `@tailwindcss/vite` | Styling |
| `typescript`, `svelte-check` | Type checking |
| `vitest`, `jsdom` | Tests |
| `eslint`, `prettier` and plugins | Linting and formatting |

A high-severity finding in any of these is a **build-integrity** problem
rather than a runtime one — which is exactly the kind of supply-chain risk
worth failing CI over, and why `pnpm audit --audit-level high` runs on every
change.

---

## Keeping them safe

| | |
| --- | --- |
| `govulncheck` | Call-graph aware, so a finding means the vulnerable function is actually reachable |
| CodeQL | Go and TypeScript, `security-and-quality` queries |
| Dependency review | Blocks vulnerable or incompatibly-licensed additions on a pull request |
| Secret scanning | Full history — a credential committed and reverted is still leaked |
| `pnpm audit` | Frontend, failing at high severity |
| Dependabot | Weekly, **grouped** |

Grouping matters. A wall of individual version bumps gets rubber-stamped, and
rubber-stamping dependency updates is how supply-chain compromises get merged.
Patch and minor updates are reviewed together; a major version is always its
own pull request, because it needs real thought.

---

## Verifying what is in a build

```bash
go list -m all                    # every module, direct and transitive
go mod graph                      # why something is present
go mod why github.com/some/thing  # what pulls it in
go mod verify                     # checksums match go.sum
govulncheck ./...                 # reachable vulnerabilities
```
