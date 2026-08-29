# Test fixtures

**Status: empty.**

Shared test data that is too large or too widely used to sit inline: sample
configurations, certificate chains for TLS tests, canned API responses.

Two rules:

- **No real credentials, ever.** Not even expired or revoked ones. CI scans
  the full history for secrets, and a fixture that looks like a key is
  indistinguishable from a leaked one.
- Prefer generating fixtures in the test over storing them here. A fixture
  file drifts away from the code it was written for; a generator does not.
