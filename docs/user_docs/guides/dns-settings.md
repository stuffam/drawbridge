---
title: DNS Settings
---

Clients ask a DNS server to turn names into addresses. The ones you choose here are written into
each client's config, so they apply while the client is connected to the VPN. Set them in
**Settings**, under **DNS for Clients**, or on the host with `sudo drawbridge server set --dns`.

## The four choices

| Choice | What clients use |
|---|---|
| **Public resolvers** | Cloudflare's `1.1.1.1` and `1.0.0.1`, and its two IPv6 addresses when the VPN has IPv6. This is the default. It works anywhere, and Cloudflare sees the names your clients look up. |
| **This server** | The server's own VPN addresses (`10.8.0.1` on a default setup, and its IPv6 counterpart). It works only if a DNS resolver on the host listens on them. |
| **Other servers** | The addresses you type, IPv4 or IPv6, separated by commas or spaces. |
| **None** | Nothing is given, so each client keeps using its own DNS. Its lookups may leak outside the tunnel. |

On the command line, `--dns` takes the addresses, `server`, or `none`:

```bash
sudo drawbridge server set --dns 9.9.9.9,149.112.112.112
sudo drawbridge server set --dns server
```

## Use a resolver on the host

A resolver such as AdGuard Home, Pi-hole, Unbound, or dnsmasq can answer for your clients, so
that, say, an ad blocker works over the VPN too. Pick **This server**, then press **Check This
Server**. The check sends a test query to each of the server's VPN addresses and says which
answer. `--dns server` does the same check and saves only the addresses that answered; `--force`
saves them anyway.

If the check finds nothing on a host that runs a resolver:

- **It has to listen on the VPN addresses.** Most resolvers listen only on the host's own
  addresses by default. For AdGuard Home, `bind_hosts` in `AdGuardHome.yaml` lists the addresses,
  or `0.0.0.0` and `::` for all of them. The resolver also has to start after the tunnel exists,
  or listen on all addresses.
- **It has to answer the VPN's subnets.** If the check says the resolver "refused the query," its
  access settings don't allow the VPN's subnets.
- **systemd-resolved doesn't count.** It listens only on `127.0.0.53`.

## After you change it

DNS is part of each client's config, so the clients that were handed a config show as
**outdated**. Give each one its config again. See
[Clients](client-configuration.md#when-a-config-is-outdated).

If names stop resolving, [Diagnostic tools](diagnostics.md) has a **DNS for clients** check. To
let Drawbridge also name your clients in AdGuard Home, see [AdGuard Home](adguard-home.md).
