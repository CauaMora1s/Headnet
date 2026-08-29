# iOS application

**Status: not implemented.** Planned for
[Phase 10](../../docs/ROADMAP.md#phase-10--mobile).

This directory is a placeholder. It contains no code, deliberately — an empty
directory that looks like a component is worse than an honest note.

## What will go here

A [Flutter](https://flutter.dev/) application over iOS `NetworkExtension` /
`NEPacketTunnelProvider`.

The desktop client daemon is not ported here. The packet tunnel extension has
a small memory budget and its own lifecycle, and fighting that would be a
losing battle.

Keys live in the Keychain, in the Secure Enclave where available.

Note that `NetworkExtension` requires a paid Apple developer account and a
specific entitlement, which is a real barrier for a community project and may
constrain how iOS builds are distributed.

See [ADR-0007](../../docs/architecture/decisions/ADR-0007-mobile-flutter.md).
