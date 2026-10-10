---
title: Server Configuration
---

The server's settings are on the **Settings** page of the web UI (the **Server Settings** icon in
the header). Change what you need and press **Save Settings**. The same settings are on the
command line: `sudo drawbridge server show` prints them, and `sudo drawbridge server set` changes
them (see [CLI tools](cli-tools.md)).

## Endpoint

Where clients connect.

- **Public address** is a domain name (keep its record current with dynamic DNS if your public
  address changes) or a public IP address, such as `vpn.example.com`.
- **Public port** is the port in clients' configs. `0` means the listen port below. Set it only if
  your router forwards a different port to the host.

## Tunnel

- **Listen port (UDP)** is the port WireGuard listens on. It's 51820 unless you change it, and
  your router's port forward has to match. Changing it disconnects every client until it has the
  new config.
- **MTU** is the largest packet the tunnel carries, from 1280 to 1500. The default, 1420, fits
  IPv4 and IPv6 paths. Lower it if large pages stall.
- **Keepalive** is how often, in seconds, a client sends a small packet so a phone behind NAT can
  still be reached. The default is 25, and 0 turns it off.
- **Client isolation** is on by default. Clients can't reach each other through the tunnel.
  Turning it off lets them, and it applies at once.

## When clients get a change

The endpoint, ports, MTU, DNS, and keepalive are part of each client's config. A client keeps what
it has until you give it the new config, so after one of them changes the clients that were handed
a config show as **outdated**. See [Clients](client-configuration.md#when-a-config-is-outdated).
Client isolation is the exception, because it's enforced on the server.

## Changes that could lock you out

A change that could cut you off from the web UI applies at once and is undone after a minute
unless you keep it. Changing the listen port is one, and [rotating the server
key](server-rotate-key.md) is the other. While one waits, a bar at the top of every page counts
down and offers **Keep Changes** and **Undo Now**. Other changes to the settings are refused
until you choose.

The change is held in the database, so if Drawbridge restarts or the host reboots first, it's
undone. On the command line, a change applies at once unless you add `--safe`, and `server
confirm` and `server revert` do what the bar's buttons do.

## Addressing

The Addressing card shows the tunnel's interface (`wg0`), the VPN's IPv4 subnet and, if it has one,
its IPv6 subnet, the server's own address in each, and what clients send through the tunnel
(everything). It's for reading: the subnets can't be changed yet while clients exist.

## DNS

The DNS servers clients are given have their own guide, [DNS settings](dns-settings.md), and so
does the optional [AdGuard Home](adguard-home.md) connection.
