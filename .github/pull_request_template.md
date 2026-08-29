## What this changes

<!-- What does this do, and why? Link the issue if there is one. -->

## Definition of Done

Every feature is held to the checklist in
[`docs/development/definition-of-done.md`](../docs/development/definition-of-done.md).
Tick what applies and delete what does not.

- [ ] Implementation is complete — no half-built paths behind a flag
- [ ] Unit tests cover the new behaviour, including its failure modes
- [ ] Integration tests, where the change crosses a boundary
- [ ] Errors are handled and the messages tell a user what to do next
- [ ] Security implications considered (see below)
- [ ] Logging added, with no secrets in it
- [ ] Documentation updated alongside the code, not afterwards
- [ ] API documentation updated (`docs/api/openapi.yaml`)
- [ ] Configuration documented (`docs/configuration.md`, `.env.example`)
- [ ] CLI or UI support, if the feature is user-facing
- [ ] Database migration included, and reversible or documented as not
- [ ] `make check` passes
- [ ] No `TODO` left behind that is not tracked by an issue

## Security

<!--
Anything that touches identity, keys, policy, enrollment or the network edge
needs a note here. If the answer is genuinely "none", say so.

- What new input does this accept, and what validates it?
- What new authorisation decision does this make?
- Could this leak key material, a token or a password into a log or response?
- Does it change what an unauthenticated caller can reach?
-->

## Honesty check

Headnet does not ship fake functionality. If this change adds a surface for
something that is not implemented yet, confirm:

- [ ] Unimplemented endpoints return `501` with the roadmap phase, or do not
      exist at all — never an empty success
- [ ] No UI element implies a working feature behind it
- [ ] No command reports success for something it did not do

## How this was verified

<!-- What did you actually run? Paste the relevant output. -->
