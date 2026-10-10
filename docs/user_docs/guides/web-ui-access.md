---
title: Web UI Access
---

The web UI is at `https://<host>:51821`. It's reachable only from your home network and the VPN,
never from the internet. The host's firewall drops other connections, and the app refuses them
too, so a mistake in one doesn't open it.

## Who can reach it

- **Your home network:** the subnets of the host's network connections that have a default route.
- **The host itself.**
- **Devices connected to the VPN.**

A device anywhere else gets no answer. If the tunnel is down, the firewall rules are gone with it,
and the app answers `403`, "the Drawbridge admin UI is only reachable from the home network and
the VPN".

Your browser warns about the certificate the first time, because Drawbridge made it itself. See
[TLS certificate](tls-certificate.md) to use your own instead.

## Let in another network

A device on another network, such as a Tailscale network, or Homepage running in Docker on the
host, is refused until you add its range:

```bash
sudo drawbridge server set --admin-allow 100.64.0.0/10
```

- A range has to be private: inside `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`,
  `100.64.0.0/10` (the range Tailscale uses), or `fc00::/7`. A public range is refused, so no
  setting can put the web UI on the internet.
- The option **replaces** the whole list. Name every range you want, separated by commas, and use
  `none` to clear it. `sudo drawbridge server show` lists them.
- Taking a range away could cut you off, so add `--safe` to undo it after a minute unless you run
  `sudo drawbridge server confirm`.

The same rule applies to a dashboard that uses an [API token](api-tokens.md).

## Don't put a reverse proxy in front of it

The web UI decides who may reach it by the address a connection comes from. A reverse proxy on the
same host (nginx, Caddy, Traefik, or a container that publishes ports 80 and 443) sends every
request from the host itself, which is always allowed, so it would put the web UI on the internet.
Open it directly at `https://<host>:51821` instead.
