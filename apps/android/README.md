# Android application

**Status: not implemented.** Planned for
[Phase 10](../../docs/ROADMAP.md#phase-10--mobile).

This directory is a placeholder. It contains no code, deliberately — an empty
directory that looks like a component is worse than an honest note.

## What will go here

A [Flutter](https://flutter.dev/) application over Android's native
`VpnService`.

The desktop client daemon is not ported here. What is reused is the API
contract, the authentication protocol, the data model and the configuration
format — not the process model, which has to follow the platform.

Keys live in the Android Keystore, hardware-backed where available.

See [ADR-0007](../../docs/architecture/decisions/ADR-0007-mobile-flutter.md).
