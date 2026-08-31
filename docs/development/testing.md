# Testing

How Headnet is tested, and what a good test looks like here.

---

## The principle

**Test the failure modes.** Most of the tests in this repository are about what
happens when something goes wrong, because that is where the interesting
behaviour of a security-relevant system lives. A happy-path test tells you the
feature exists; a failure-path test tells you it is safe.

Concretely, the tests that matter most so far are things like:

- Does an unauthenticated health endpoint leak a file path?
- Does an OIDC code end up in a log?
- Can rotating a header reset the rate-limit bucket?
- Does a panic disclose a stack trace to the client?
- Does anything claim a VPN connection that does not exist?

---

## Layers

| Layer | Where | What it covers |
| --- | --- | --- |
| **Unit** | Next to the code | Logic in isolation |
| **Integration** | `internal/server`, `test/integration` | Components together, real database |
| **End-to-end** | `.github/workflows/ci.yml`, `test/e2e` | A built binary actually running |
| **Security** | Throughout, marked by comment | Specific threat-model properties |

### Unit

Table-driven, in a `_test.go` file next to the code. External test packages
(`package config_test`) where possible, so tests exercise the public API
rather than reaching into internals.

### Integration

The server tests build a real `Server` over a real database, migrate it, and
drive it through `httptest`. No mocks for the database — a mocked database
tests the mock.

**Both backends are exercised.** `storagetest.Open(t)` returns an isolated
in-memory SQLite database by default, and a uniquely-schema'd PostgreSQL
database when `HEADNET_TEST_POSTGRES_DSN` is set. CI runs one job each way.

To run against PostgreSQL locally:

```bash
docker run -d --name headnet-pgtest \
  -e POSTGRES_USER=headnet -e POSTGRES_PASSWORD=headnet -e POSTGRES_DB=headnet \
  -p 55432:5432 postgres:17-alpine
```

```bash
HEADNET_TEST_POSTGRES_DSN="postgres://headnet:headnet@127.0.0.1:55432/headnet?sslmode=disable" go test -count=1 ./internal/...
```

This is worth doing before pushing anything that touches SQL. It has already
paid for itself twice: it caught test fixtures using `?` placeholders that
only SQLite accepts, and a genuine concurrency bug in the address allocator
that SQLite structurally cannot reach — under SQLite the pool is held to a
single connection, so allocations serialise and never contend.

Tests that assert SQLite-specific behaviour — a pragma, a `sqlite_master`
query — call `storagetest.SkipUnlessSQLite(t)`.

In-memory databases are given random names from the CSPRNG so parallel tests
cannot see each other's schema. This was a real bug: an earlier version used a
nanosecond timestamp, and Windows' clock granularity is coarse enough that
parallel opens collided and silently shared a database.

### End-to-end

CI builds the binaries, starts the server, and checks that a fresh install
actually comes up: the database is created and migrated, the documented
endpoints answer, and the CLI reports honestly. Unit tests cannot catch a
broken migration embed or a bad default; this does.

Network-level tests using containers to simulate NAT arrive with
[Phase 4](../ROADMAP.md#phase-4--nat-traversal).

### Security tests

Not a separate suite — they live with the code and are marked by a comment
saying what property they protect:

```go
func TestRateLimitMiddlewareCannotBeEvadedWithHeaders(t *testing.T) {
	// If forwarding headers were trusted, rotating one would reset the bucket
	// and the limiter would protect nothing.
```

That comment is load-bearing. Without it, a future contributor tidying up
tests may not realise why the test exists and "simplify" the property away.

---

## Running them

```bash
make test          # everything
make test-go       # Go
make test-web      # frontend
make test-race     # race detector (needs a C compiler; CI always runs it)
make cover         # coverage report at coverage.html

go test ./internal/storage/...            # one package
go test -run TestMigrate ./internal/...   # one test
go test -v -count=1 ./...                 # verbose, no cache
go test -shuffle=on ./...                 # catch order dependence
```

---

## Writing a good test here

### Name it after the property

The name should say what is protected, so a failure in CI is diagnosable from
the name alone.

```go
// Good — a failure tells you what broke and why it matters
func TestPlainHTTPIsRefusedInProduction(t *testing.T)
func TestRevokedDeviceLosesAccessImmediately(t *testing.T)
func TestUnimplementedCommandsFailHonestly(t *testing.T)

// Bad
func TestValidate3(t *testing.T)
func TestServer(t *testing.T)
```

### Say what went wrong, and what was expected

```go
// Good
t.Fatalf("server.listen = %q, want the environment to win over the file", cfg.Server.Listen)

// Bad
t.Fatal("wrong value")
```

### Table-driven, with named cases

```go
tests := []struct {
	name    string
	cidr    string
	wantErr string
}{
	{"loopback", "127.0.0.0/8", "reserved"},
	{"host bits set", "100.100.0.5/16", "host bits"},
	{"too large", "96.0.0.0/4", "prefix length"},
}
for _, tt := range tests {
	t.Run(tt.name, func(t *testing.T) {
		t.Parallel()
		...
	})
}
```

### Use `t.Context()` and `t.TempDir()`

Both are cleaned up automatically, and `t.Context()` is cancelled when the
test ends, so a leaked goroutine surfaces rather than hanging.

### Do not test implementation

Test what a caller can observe. A test that breaks when you rename a private
field is a test that makes refactoring expensive without making the code safer.

### Explain a non-obvious assertion

```go
func TestRedactKeepsUnsetSecretsVisiblyEmpty(t *testing.T) {
	// An operator reading a redacted dump has to be able to tell "the secret
	// is set, I just cannot see it" from "the secret is missing entirely",
	// because forgetting to supply one is the more common mistake.
```

---

## Frontend

Vitest with jsdom.

```bash
pnpm --filter @headnet/web test
pnpm --filter @headnet/web test:watch
```

The API client is tested against stubbed `fetch` responses covering the cases
that actually happen in production: a 503 from `/health` that is a real answer
rather than a failure, an HTML error page from a proxy, an unreachable server,
a malformed body.

Note the `expectApiError` helper. Writing `.catch(e => e as ApiError)` would
leave the value typed as a union with the success type, so a test could
silently assert against a successful response — `svelte-check` caught exactly
that.

Route matching is tested as a pure function, separately from the reactive
navigation state, so it needs neither a DOM nor a Svelte runtime.

One frontend test bug worth remembering: `new Response('', { status: 204 })`
throws, because a 204 cannot carry a body — not even an empty string. The stub
surfaced that as a bogus network error rather than as the case under test, so
the helper now passes `null` for null-body statuses.

Component tests with Testing Library arrive as the UI grows; Playwright is
planned for end-to-end coverage. In the meantime the browser path is walked by
hand against a real server before each UI change lands.

---

## Coverage

There is no coverage threshold, deliberately. A percentage target produces
tests written to raise the percentage, which are the least useful kind.

What matters:

- Every error path is exercised.
- Every security property has a test.
- Every bug fixed gets a regression test.

```bash
make cover
```

---

## CI

Every push and pull request runs:

| Job | What |
| --- | --- |
| Go lint | `gofmt`, `go vet`, `go mod tidy` check, `golangci-lint` |
| Go test | Linux, macOS and Windows, shuffled |
| Go test (PostgreSQL) | Linux, against a real PostgreSQL 17 service |
| Go test (race) | Linux, `CGO_ENABLED=1` |
| Go build | Seven platform targets |
| Web | Lint, type check, test, build |
| Smoke | A real server, real endpoints, real CLI |
| Docker | Image builds and runs |

Plus a security workflow: `govulncheck`, CodeQL, dependency review, secret
scanning over full history, and a frontend audit.

Three operating systems from the start is deliberate. The client will manage
network interfaces on all three, and path handling, signals and clock
granularity differ in ways that are much cheaper to find now than in Phase 2.

---

## Fixing a bug

1. **Write a failing test first.** It proves you understand the bug, and it
   stops the bug coming back.
2. Fix it.
3. Confirm the test passes and nothing else broke.
4. If the bug had a security consequence, say so in the test comment.

## Adding a feature

1. Write the tests as you write the code, not afterwards.
2. Cover the failure modes, not just the success path.
3. Add an end-to-end check if the feature is user-visible.
4. Update the [Definition of Done](definition-of-done.md) checklist in the
   pull request.
