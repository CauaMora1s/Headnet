# End-to-end tests

**Status: covered in CI, not yet here.**

The smoke test in
[`.github/workflows/ci.yml`](../../.github/workflows/ci.yml) currently does
this job: it builds the binaries, starts a real server, and verifies that a
fresh install comes up — the database is created and migrated, the documented
endpoints answer, and the CLI reports honestly about what it cannot do. Unit
tests cannot catch a broken migration embed or a bad default; that can.

This directory is for end-to-end tests once there is a user journey to walk:

| | Phase |
| --- | --- |
| Bootstrap, sign in, enrol a device | 1–2 |
| Playwright coverage of the web UI | 1 |
| Full journey: install, log in, connect, reach a peer | 3 |
