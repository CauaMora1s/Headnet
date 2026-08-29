# Integration tests

**Status: empty.** Integration coverage that fits inside `go test` currently
lives next to the code — `internal/server` builds a real server over a real
(in-memory) database and drives it through `httptest`.

This directory is for tests that need more than a Go process:

| | Phase |
| --- | --- |
| Client-to-server enrolment against a real daemon | 2 |
| Device-to-device connectivity in containers | 3 |
| NAT simulation: full-cone, restricted, port-restricted, symmetric, CGNAT | 4 |
| Relay fallback when direct connection is impossible | 5 |
| Policy enforcement across real peers | 6 |

The failure cases matter as much as the successes. The point of the NAT suite
is knowing precisely when a direct connection is impossible, not only that it
sometimes works.
