# CLI reference

`headnet` manages this device's membership of a Headnet private network.

> **Most of it is not implemented yet.** The commands are declared so the
> shape of the tool is visible and stable, and every unimplemented one says so
> and exits non-zero. None of them ever pretends to succeed.

---

## Working today

### `headnet version`

Prints the build identity. Include this in any bug report.

```
$ headnet version
0.1.0 (commit a1b2c3d, built 2026-08-29T12:00:00Z, go1.25.0 linux/amd64)
```

A `-dirty` suffix on the commit means the binary was built from a modified
working tree — worth knowing when someone reports a problem you cannot
reproduce.

### `headnet diagnostics`

Runs every check this build can perform and explains each result.

```
headnet diagnostics [-server URL] [-json] [-timeout DURATION]
```

| Flag | Default | |
| --- | --- | --- |
| `-server` | *(none)* | Control server base URL to test |
| `-json` | `false` | Machine-readable output |
| `-timeout` | `5s` | Per-check network timeout |

```
$ headnet diagnostics -server https://vpn.example.com

  ✓  CLI build                 0.1.0 (a1b2c3d, linux/amd64)
  ✓  DNS                       vpn.example.com resolves to 203.0.113.10
  ✓  Control server            vpn.example.com is reachable and reports "ok" (version 0.1.0)
  ✓  Protocol compatibility    agreed on protocol v1 (this build speaks 1–1, the server speaks 1–1)
  –  Client daemon             not built yet (Phase 2)
  –  WireGuard interface       not built yet (Phase 2)
  –  Direct peer connectivity  not built yet (Phase 4)
  –  Relay fallback            not built yet (Phase 5)

Details:
  Client daemon: The background daemon that holds this device's keys and manages WireGuard is not implemented.
  ...
```

The three markers mean different things, and the distinction is the point of
the tool:

| | |
| --- | --- |
| `✓` | The check ran and passed |
| `✗` | The check ran and found a real problem |
| `–` | Skipped — either it needed input you did not give, or the subsystem is not built |

**A skipped check is never reported as passing.** An unbuilt subsystem shown
as healthy would be exactly the kind of false reassurance this project avoids.

Every failure comes with a remedy. A diagnostic that only says what is broken
is an error message.

For scripting:

```bash
headnet diagnostics -server https://vpn.example.com -json | jq '.checks[] | select(.state == "fail")'
```

---

## Not implemented yet

These exist in the command tree and in `--help`, and every one of them exits
`3` with an explanation:

```
$ headnet connect
headnet connect: connecting to a network is not implemented yet.

It is planned for Phase 2 of the roadmap:
  https://github.com/CauaMora1s/Headnet/blob/main/docs/ROADMAP.md
```

| Command | | Phase |
| --- | --- | --- |
| `login` | Authenticate this device | [2](../ROADMAP.md#phase-2--client-mvp) |
| `logout` | Sign out and forget local credentials | 2 |
| `up` | Bring the network up, optionally with a setup key | 2 |
| `connect` | Connect to the private network | 2 |
| `disconnect` | Disconnect | 2 |
| `status` | This device's connection status | 2 |
| `devices` | List devices on this network | [3](../ROADMAP.md#phase-3--device-to-device-networking) |
| `peers` | Peer connection detail | 3 |
| `ping` | Check reachability of another device | 3 |
| `routes` | Manage advertised subnet routes | [8](../ROADMAP.md#phase-8--routes) |

Administrative commands (`headnet admin users`, `admin devices`,
`admin routes`, `admin policies`) arrive with the features behind them.

---

## Exit codes

Part of the tool's contract, so scripts can rely on them.

| | Meaning |
| --- | --- |
| `0` | Success |
| `1` | The command ran and failed |
| `2` | Usage error — unknown command, bad flag, missing argument |
| `3` | The feature is not implemented in this build |
| `4` | A dependency (the daemon, the control server) was unreachable |

Code `3` exists specifically so a script can distinguish "this build does not
have that feature" from "that failed" — which matters a great deal while most
of the CLI is unbuilt:

```bash
headnet connect
case $? in
  0) echo "connected" ;;
  3) echo "this build cannot connect yet" ;;
  4) echo "the daemon is not running" ;;
  *) echo "failed" ;;
esac
```

---

## Streams

- **stdout** carries command output. Safe to pipe.
- **stderr** carries errors and diagnostics.

Unimplemented commands write nothing to stdout, so a script consuming output
never receives an error message as data.

---

## Help

```bash
headnet -h              # the whole command tree
headnet <command> -h    # one command and its flags
```

Commands not yet implemented are marked `(planned, Phase N)` in the listing,
so you can tell what works today from what is merely declared.

---

## Configuration

The CLI is currently stateless — it stores nothing and reads no configuration
file. Everything is passed as flags.

From Phase 2 it will talk to the local daemon over a unix socket (or a named
pipe on Windows), and the daemon will hold the device identity and
configuration. That boundary is deliberate: the daemon is the only component
that needs privilege, so the CLI stays unprivileged.
