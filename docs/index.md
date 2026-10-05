# Drawbridge

Drawbridge is a self-hosted web manager for a WireGuard VPN server. It installs natively (no
Docker) on Debian-family Linux with systemd, for arm64 and amd64, and it supports both IPv4 and
IPv6. It's tested on a Raspberry Pi 5 running Debian 13.

You manage the tunnel and its clients from a web UI at `https://<host>:51821` that only your home
network and the VPN can reach, from the command line (`sudo drawbridge client add phone --qr`), or
through the same authenticated API.

## What it does

- **Clients:** add, remove, rename, and pause them, hand out a config, a download, or a QR code,
  and rotate a client's keys.
- **Connections and traffic:** see who is connected, their session history, and their traffic
  over time, with a log of every change.
- **Server settings:** the endpoint, addresses, and MTU, with a safety net that undoes a change
  that cuts you off unless you keep it.
- **DNS:** public resolvers by default, or a resolver on the host such as AdGuard Home, which can
  also have your clients' names kept in sync.
- **Operations:** `drawbridge doctor` checks the host and network, encrypted backups, optional
  two-factor authentication, and your own TLS certificate for the web UI.

The VPN doesn't depend on the web UI: the tunnel starts at boot from the database, and keeps
running if the UI stops or is upgraded.

## Before you install

Read [Requirements and known roadblocks](REQUIREMENTS.md). It lists what Drawbridge needs from
the host and your network, and the setups where it needs a workaround, for example a host that
configures IPv6 with ifupdown, or a router that won't forward UDP 51820. After you install, run
`sudo drawbridge doctor` to check for most of them.

Then follow [Install Drawbridge](install.md): get the package, install it, set it up in your
browser, and connect a first client.

## Guides

- [Install Drawbridge](install.md): from a bare host to a phone on your VPN, and how to upgrade and
  remove it.
- [Backup and restore](backup-restore.md): make an encrypted backup, keep it, and restore it onto
  a fresh host.
- [Two-factor authentication](two-factor.md): ask for a code from an authenticator app at login.
- [The web UI's TLS certificate](tls-certificate.md): serve your own certificate instead of the
  self-signed one.
- [Read-only API tokens](api-tokens.md): let a dashboard such as Homepage read the status.

## For contributors

- [The plan](PLAN.md) is the specification: the architecture, the feature spec, the security
  design, and the roadmap.
- [Decisions](adr/README.md) explains each of the project's major decisions and what follows from
  it.
- [The manual checklist](MANUAL_CHECKLIST.md) records what has run on real hardware, and what
  hasn't.

The source is at [github.com/stuffam/drawbridge](https://github.com/stuffam/drawbridge), under
the Apache License 2.0.

"WireGuard" is a registered trademark of Jason A. Donenfeld. Drawbridge isn't affiliated with or
endorsed by the WireGuard project.
