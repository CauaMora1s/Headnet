# Contributing to Headnet

Thanks for considering it. Headnet is early, which means the foundations are
still soft and a well-argued change can shape the project rather than fit
around it.

Please read [SECURITY.md](SECURITY.md) before reporting anything that looks
like a vulnerability, and the [threat model](docs/security/threat-model.md)
before touching identity, keys, policy or the network edge.

## Getting set up

You need **Go 1.25+**, and **Node 20+** with **pnpm** for the web UI. Full
instructions, including optional tools, are in
[docs/development/setup.md](docs/development/setup.md).

```bash
git clone https://github.com/headnet/headnet.git
cd headnet
pnpm install
make check     # or .\scripts\dev.ps1 check on Windows
```

`make check` runs everything CI runs. If it passes locally it should pass in
CI; if it does not, that is a bug in the Makefile worth reporting.

## How work gets done here

The order matters more than the speed. For anything beyond a typo:

```
Understand → Plan → Implement → Test → Security review
          → Document → Review the architecture → Commit
```

Before writing code for a feature:

1. **Read the relevant architecture documentation.** Most design questions are
   already answered in [`docs/architecture/`](docs/architecture/) or an
   [ADR](docs/architecture/decisions/).
2. **Check the roadmap.** [`docs/ROADMAP.md`](docs/ROADMAP.md) lists each
   phase with its dependencies, security considerations and Definition of
   Done. Working out of order usually means building on something that does
   not exist yet.
3. **Open an issue or discussion first** for anything substantial. It is much
   cheaper to disagree about a design in a paragraph than in a thousand lines
   of Go.

Then: implement the smallest complete version, write the tests, run the
static analysis, think about the security implications, update the
documentation, and only then open the pull request.

### Please do not send enormous pull requests

A change that touches authentication, the API, the database schema and the UI
at once cannot be reviewed properly, and security-relevant code that cannot be
reviewed properly should not be merged. Split it.

## Definition of Done

No feature is finished until every applicable box is ticked. The full list,
with what each one means, is in
[docs/development/definition-of-done.md](docs/development/definition-of-done.md).

- [ ] Implementation complete — no half-built paths
- [ ] Unit tests, including the failure modes
- [ ] Integration tests where the change crosses a boundary
- [ ] Errors handled, with messages that tell a user what to do next
- [ ] Security implications considered and written down
- [ ] Logging added, with no secrets in it
- [ ] Metrics, where the feature has an operational signal worth watching
- [ ] Documentation written alongside the code
- [ ] API documentation updated (`docs/api/openapi.yaml`)
- [ ] Configuration documented (`docs/configuration.md`, `.env.example`)
- [ ] CLI or UI support if the feature is user-facing
- [ ] Migration included if the schema changed
- [ ] CI passes
- [ ] No untracked `TODO` left behind

## The rules that are not negotiable

### 1. Do not fake functionality

This is the one we will always push back on.

A VPN that reports success without carrying traffic can convince someone they
are protected when they are not. So an unimplemented feature must be visibly
unimplemented:

- API endpoints return `501` with the roadmap phase, or do not exist. Never an
  empty success.
- CLI commands exit non-zero and say what is missing.
- The UI says a thing is unbuilt rather than showing an empty table of it.
- Diagnostics report unbuilt subsystems as *skipped*, never as passing.

There are tests asserting this. Please do not work around them.

### 2. Do not invent cryptography

Headnet uses WireGuard for the data plane, TLS for the control plane, and
Argon2id for password hashing. If you find yourself writing a key exchange, a
cipher mode or a token format with a MAC in it, stop and open a discussion.

### 3. Private keys stay on devices

The server stores public keys. Any design that requires a private key to reach
the server is wrong, however convenient.

### 4. Default deny

Access-control changes default to refusing. A policy bug that denies traffic
is an inconvenience; one that allows it is a breach.

### 5. Never log a secret

Not private keys, passwords, session tokens, setup keys, OIDC client secrets
or database DSNs. Configuration is logged through `config.Redact`. Query
strings are not logged at all, because OIDC and enrolment flows put codes in
them.

## Adding a dependency

Headnet has three direct Go dependencies. That is on purpose: everything on
the request path is code we are responsible for.

Before adding one, answer these in the pull request:

1. Is the standard library genuinely insufficient? (Go 1.22+ `net/http`
   routing removed the usual reason for a router; `log/slog` removed the usual
   reason for a logging library.)
2. Is it actively maintained?
3. Is the licence compatible? See [docs/licensing.md](docs/licensing.md) —
   copyleft dependencies cannot go in `packages/`.
4. Does it have known vulnerabilities?
5. What does it pull in transitively?
6. Does it create vendor lock-in?

A dependency that is smaller than the code it replaces is usually not worth
it.

## Code style

### Go

- `gofmt` decides formatting. `make fmt` applies it.
- `golangci-lint` decides the rest; the configuration is
  [`.golangci.yml`](.golangci.yml).
- Exported identifiers have doc comments. Say *why*, not *what* — the code
  already says what.
- Wrap errors with context: `fmt.Errorf("binding %s: %w", addr, err)`.
- Sentinel errors for conditions a caller must branch on; `errors.Is`/`As` to
  test them, never string matching.
- Every blocking call takes a `context.Context`.
- Table-driven tests, with names that read as sentences.

Error messages are read by operators at three in the morning. Write them for
that reader:

```go
// Not this:
return errors.New("invalid config")

// This:
return fmt.Errorf("server.base_url: %q is not a valid URL: %w", raw, err)
```

### TypeScript and Svelte

- Prettier and ESLint decide; `pnpm format` applies them.
- `strict` TypeScript. No `any`.
- Colour must never be the only carrier of meaning.
- Keyboard focus stays visible. This is an administrative interface for
  security settings and it has to be operable without a mouse.

## Commits

[Conventional Commits](https://www.conventionalcommits.org/):

```
feat(server): add device enrollment endpoint
fix(auth): prevent token replay after logout
security(api): rate-limit the login endpoint
docs(architecture): document the relay handshake
test(network): cover peer reconnection
refactor(storage): extract the migration ledger
chore(deps): update pgx to v5.10.0
```

Scopes in use: `server`, `client`, `cli`, `web`, `auth`, `devices`, `network`,
`wireguard`, `relay`, `acl`, `dns`, `routes`, `storage`, `api`, `protocol`,
`config`, `docs`, `ci`, `deps`.

Keep commits small and logically isolated. A commit that changes one thing can
be reverted; one that changes six cannot.

## Pull requests

1. Branch from `main`.
2. Make the change, with tests and documentation.
3. Run `make check`.
4. Fill in the pull request template honestly — particularly the security and
   honesty sections.
5. Expect review comments on anything touching security. That is not
   distrust; it is the process working.

## Testing

Details in [docs/development/testing.md](docs/development/testing.md). The
short version:

- Test behaviour, not implementation.
- Test the failure modes, not just the happy path. Most of the tests in this
  repository are about what happens when something goes wrong.
- A test name should say what property is being protected:
  `TestPlainHTTPIsRefusedInProduction`, not `TestValidate3`.
- Where a test encodes a security property, say so in a comment, so a future
  contributor knows why they must not "simplify" it away.

## Documentation

Write it with the code, not afterwards. A feature merged without documentation
is a feature nobody can use.

- User-facing changes: `docs/` and the relevant guide.
- API changes: `docs/api/openapi.yaml`.
- Configuration changes: `docs/configuration.md`, `headnet.example.yaml` and
  `.env.example`.
- Architectural decisions: a new
  [ADR](docs/architecture/decisions/). Copy the format of an existing one.

## Code of Conduct

By participating you agree to the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Licensing your contribution

Contributions to `packages/` are licensed under Apache-2.0; everything else
under AGPL-3.0-or-later. By opening a pull request you confirm you have the
right to contribute the code under those terms. There is no CLA.

See [docs/licensing.md](docs/licensing.md) for why the split exists.
