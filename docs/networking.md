# Networking

How Headnet addresses devices and gets packets between them.

> **Most of this is designed but not built.** Sections are marked with the
> roadmap phase that delivers them. The addressing rules are enforced today;
> everything about actually moving packets is not.

---

## Addressing

Every device gets an address from a pool the administrator configures.

```yaml
network:
  ipv4_cidr: "100.100.0.0/16"
  ipv6_cidr: "fd7a:115c:a1e0::/48"
```

**Implemented today: validation and allocation.** Addresses are handed out
from these pools, one per enabled family, and released when their owner goes
away. What does not exist yet is anything to own one — devices arrive later in
[Phase 1](ROADMAP.md#phase-1--server-mvp).

### Why `100.100.0.0/16`

The default sits inside `100.64.0.0/10`, the range reserved for carrier-grade
NAT. That is the conventional choice for an overlay network for a practical
reason: it is almost never present on a home or office LAN, so it rarely
collides with the networks users are already on.

Compare the alternatives:

| Range | Problem |
| --- | --- |
| `192.168.0.0/16` | Nearly every home router uses it |
| `10.0.0.0/8` | Extremely common in corporate networks and cloud VPCs |
| `172.16.0.0/12` | Docker's default range |
| `100.64.0.0/10` | Used only by ISPs, essentially never on a LAN |

A collision is not a cosmetic problem. If the VPN hands a device an address
that also exists on its local network, the routing table has two claims on it
and traffic goes to the wrong place — intermittently, and confusingly.

### IPv6

The default is a unique-local prefix (`fd00::/8`). IPv6 is optional; set the
prefix empty for an IPv4-only network.

### What validation refuses

The rules are enforced at start-up, and are worth knowing because the error
messages are specific:

- **Reserved ranges.** Loopback, link-local, multicast and reserved space.
  Numbering a device from `127.0.0.0/8` would shadow its own loopback traffic.
- **Host bits set.** `100.100.0.5/16` is rejected, with the corrected value in
  the message. Silently masking it would mean the pool you read back is not
  the one you wrote.
- **Prefix sizes** outside `/8`–`/30` for IPv4, `/16`–`/120` for IPv6.
- **Overlap** between the IPv4 and IPv6 pools.
- **Mixed families** — an IPv6 prefix in the IPv4 field.

> **Choose the pool before you roll out.** Changing it after devices are
> enrolled means re-addressing every one of them.

---

## Getting packets between devices

The order of preference, and the reason for it:

```
1. Direct, on the same LAN          fastest, no NAT involved
2. Direct, across NAT               fast; needs hole punching
3. Relayed                          slower; only when 1 and 2 are impossible
```

### Direct connections — Phase 3 and 4

Two devices on the same network find each other by their local addresses. Two
devices behind different NATs need help, which is what
[Phase 4](ROADMAP.md#phase-4--nat-traversal) provides:

```
Each peer discovers its candidate addresses
  · local addresses from its own interfaces
  · a server-reflexive address, via STUN
        ↓
Candidates exchanged through the control plane
        ↓
Both peers send simultaneously (UDP hole punching)
        ↓
The first path to complete a WireGuard handshake wins
        ↓
Probing continues, in case a better path appears
```

Two things worth stating plainly:

- **A discovered endpoint is a hint, not a fact.** A malicious or spoofed STUN
  server can lie about a reflexive address. Only a completed WireGuard
  handshake confirms a path.
- **No security property depends on NAT.** NAT is not a firewall. All
  authentication and authorisation lives in WireGuard and the policy layer.

The implementation will follow published approaches — Tailscale's and
WebRTC's ICE — rather than being invented here, and an ADR will record which
parts are adopted and which are simplified.

### When direct fails — Phase 5

Some combinations cannot be punched through: two symmetric NATs, or a firewall
that blocks outbound UDP entirely. Then traffic goes through a relay.

```
Device A  ──►  Relay  ──►  Device B
             forwards ciphertext
```

**The relay cannot read the traffic.** It forwards WireGuard packets it holds
no key for. A compromised relay sees which peers communicate, when, and how
much — metadata, not content. That limitation is real and is documented rather
than glossed over; Headnet does not claim traffic-analysis resistance.

Relaying is fallback only, the client keeps probing for a direct path, and the
client tells you when it is relaying. You are entitled to know your traffic is
taking a longer path through another machine. See
[ADR-0008](architecture/decisions/ADR-0008-relay-architecture.md).

---

## Subnet routes — Phase 8

One device can make a whole network reachable to the others:

```
Your laptop  ──►  Headnet  ──►  Subnet router  ──►  192.168.1.0/24
                                                    (your home LAN)
```

Useful for reaching a NAS, a printer, or a cloud VPC without installing
Headnet on everything.

**Advertising a route is not the same as having it approved.** A device may
advertise; an administrator must approve. Without that separation, one
compromised device could claim `0.0.0.0/0` and become a man in the middle for
the entire network. This is why route approval is a hard requirement rather
than a convenience.

---

## Exit nodes — Phase 11

Routing *all* internet traffic through another device.

This is deliberately a separate feature from subnet routing, with its own
authorisation and its own unmistakable UI. Sending someone's entire internet
traffic through a machine is a fundamentally different trust decision from
letting them reach a private service, and treating it as a variation on subnet
routing would be a mistake.

---

## DNS — Phase 7

```
ssh nas          instead of    ssh 100.100.0.7
```

Names derived from device hostnames within a network-scoped domain. Split DNS
so that internal names resolve over the VPN while everything else keeps
working normally.

Name assignment is the server's job, not the device's — otherwise a device
could claim another's name.

---

## What Headnet needs from your network

### Control plane

Outbound HTTPS (443, or wherever you host it) from every device. That is all.

### Data plane — Phase 3 onwards

Outbound UDP for WireGuard. Devices do not need inbound ports or port
forwarding; that is what NAT traversal is for.

If outbound UDP is blocked entirely — some corporate networks do this — direct
connections are impossible and traffic will relay.

### Firewall

Nothing special. Headnet is designed for devices behind NAT with no inbound
connectivity, which is the normal situation for a laptop or a phone.

---

## MTU

WireGuard adds 60 bytes of overhead (IPv4) or 80 (IPv6). Headnet will set the
interface MTU accordingly.

If you see connections that establish but stall on large transfers, MTU is the
usual cause — typically a path with a smaller MTU than expected, such as a
PPPoE link. `headnet diagnostics` will grow an MTU check in Phase 4.

---

## Further reading

- [Architecture](architecture.md) — how the control and data planes separate
- [Threat model](security/threat-model.md) — what each component can and
  cannot see
- [ADR-0002](architecture/decisions/ADR-0002-use-wireguard.md) — why WireGuard
- [ADR-0008](architecture/decisions/ADR-0008-relay-architecture.md) — why the
  relay works the way it does
