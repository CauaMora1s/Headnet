# Development setup

## Requirements

| | Version | Needed for |
| --- | --- | --- |
| [Go](https://go.dev/dl/) | 1.25+ | Server, CLI, everything backend |
| [Node](https://nodejs.org/) | 20+ | Web UI |
| [pnpm](https://pnpm.io/installation) | 10+ | Web UI |
| [Git](https://git-scm.com/) | any | |

Go 1.25 specifically: the SQLite driver requires it, and `go.mod` records
that. CI reads the version from `go.mod` so the toolchain cannot drift.

### Optional

| | For |
| --- | --- |
| [golangci-lint](https://golangci-lint.run/welcome/install/) | The full lint suite. `make lint` skips it with a note if absent. |
| [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck) | Vulnerability scanning. `make vuln` installs it if missing. |
| [Docker](https://docs.docker.com/get-docker/) | Container builds and, later, integration tests |
| A C compiler (gcc/clang) | **Only** for `-race`. See below. |

---

## Getting started

```bash
git clone https://github.com/headnet/headnet.git
cd headnet
pnpm install
make check
```

`make check` runs everything CI runs. If it passes locally it should pass in
CI; if it does not, that is a bug in the Makefile worth reporting.

### Windows

Windows does not ship `make`. [`scripts/dev.ps1`](../../scripts/dev.ps1)
mirrors every target:

```powershell
.\scripts\dev.ps1 help
.\scripts\dev.ps1 check
.\scripts\dev.ps1 dev
```

The two are kept in step deliberately: a contributor on Windows has to be able
to run exactly what CI runs before pushing.

---

## Everyday commands

```bash
make dev          # run the control plane, text logs, SQLite
make dev-web      # run the web UI dev server (proxies the API to :8080)

make test         # every test suite
make test-go      # Go only
make test-web     # frontend only
make cover        # coverage report at coverage.html

make lint         # go vet, golangci-lint, eslint, svelte-check
make fmt          # format everything
make fmt-check    # fail if anything is unformatted

make build        # binaries into ./bin
make vuln         # scan Go dependencies
make tidy         # go mod tidy && go mod verify

make check        # all of the above that CI cares about
```

---

## Running the two halves together

The Go server and the Vite dev server want two terminals:

```bash
# Terminal 1
make dev
# → http://localhost:8080

# Terminal 2
make dev-web
# → http://localhost:5173
```

Vite proxies `/api`, `/health` and `/ready` to `:8080`, so the frontend runs
on the same origin as the API. That is not just convenience: it means cookies,
CSRF handling and same-origin fetches behave in development the way they will
in production, where the Go binary serves both.

If your server is on a different port:

```bash
HEADNET_DEV_SERVER=http://127.0.0.1:9090 pnpm --filter @headnet/web dev
```

---

## About `-race`

The race detector requires cgo, and therefore a C compiler. Since Headnet
builds with `CGO_ENABLED=0` everywhere else, `make test-race` sets
`CGO_ENABLED=1` explicitly:

```bash
make test-race
```

Without a C toolchain this fails with `cgo: C compiler "gcc" not found`. That
is expected — it is not a broken checkout. **CI runs the race detector on
Linux for every change**, so a race will be caught even if you cannot run it
locally.

If you are working on anything concurrent and want it locally: install
build-essential (Debian/Ubuntu), Xcode command line tools (macOS), or
[TDM-GCC](https://jmeubank.github.io/tdm-gcc/) / MSYS2 (Windows).

---

## Project layout

```
apps/server/      control plane
apps/cli/         command-line interface
apps/web/         Svelte web UI
packages/         shared contracts (Apache-2.0)
internal/         control-plane implementation (AGPL-3.0)
docs/             this documentation
deploy/           Docker, Compose, systemd
scripts/          development helpers
```

More detail, including the dependency rules between layers, in
[architecture.md](architecture.md).

---

## Editor setup

An [`.editorconfig`](../../.editorconfig) is in the repository; most editors
pick it up automatically.

**VS Code.** Install the Go and Svelte extensions. Recommended settings:

```json
{
  "go.lintTool": "golangci-lint",
  "go.formatTool": "gofmt",
  "editor.formatOnSave": true,
  "[svelte]": { "editor.defaultFormatter": "svelte.svelte-vscode" },
  "[typescript]": { "editor.defaultFormatter": "esbenp.prettier-vscode" }
}
```

**GoLand / WebStorm.** Enable `gofmt` on save and point the Go linter at
`golangci-lint`.

---

## Common problems

**`go: cannot find module` after pulling.** Run `go mod download`.

**`golangci-lint: command not found`.** Optional; `make lint` skips it with a
note. Install it if you want the full suite locally.

**`cgo: C compiler "gcc" not found`.** Only `-race` needs it. See above.

**pnpm reports an ignored build script for esbuild.** The root `package.json`
has an `onlyBuiltDependencies` allowlist. Run `pnpm install` again, or
`pnpm rebuild esbuild`.

**Port 8080 is busy.** `HEADNET_SERVER_LISTEN=:9090 make dev`.

**The database is in a strange state during development.** Delete
`./data/headnet.db` and restart. Migrations recreate it. Never do this to
anything real.

---

## Before you open a pull request

```bash
make check
```

Then read the [Definition of Done](definition-of-done.md) and fill in the pull
request template honestly — particularly the security and honesty sections.

If your change touches identity, keys, policy or the network edge, read the
[threat model](../security/threat-model.md) first.
