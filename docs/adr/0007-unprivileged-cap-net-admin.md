# ADR 0007: Run unprivileged, with only CAP_NET_ADMIN

- **Status:** Accepted (2026-09-26); amended 2026-10-09
- **Plan reference:** docs/PLAN.md §3, D7

## Context

Whoever controls the admin UI can already reach the home network, so a compromised daemon must not
also become root on the host. Configuring WireGuard, addresses, routes, and nftables needs
`CAP_NET_ADMIN` and nothing more.

## Decision

Run both units as the `drawbridge` system user, created from `/usr/lib/sysusers.d/drawbridge.conf`,
with `CAP_NET_ADMIN` as the only capability and heavy systemd sandboxing. Anything that needs root
(sysctls, the NetworkManager drop-in, the system user) happens once in the package's `postinst`. The
daemon runs no shell commands and offers no hooks; the only external command is `nft -f`, called
with an argument list.

## Consequences

- M0's unit holds no capabilities at all (`CapabilityBoundingSet=` empty). M1 grants `CAP_NET_ADMIN`
  when WireGuard code arrives.
- `systemd-analyze security --offline=true` rated the M0 unit 1.5 ("OK") on 2026-09-26. The
  remaining exposure is deliberate: network access, netlink and Unix sockets, and `/proc/sys` for
  diagnostics.
- `PrivateUsers=` stays off, because a user namespace would make `CAP_NET_ADMIN` useless on the
  host's network.
- **Amended 2026-10-09: nft's other arguments.** The only external program is `nft`, but not only
  with `-f`: the drift check reads the table with `nft -j list table`, and the doctor reads the
  ruleset with `nft -j list ruleset`. Every call uses an argument list, and none uses a shell.
