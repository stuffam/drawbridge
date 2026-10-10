---
title: Home
hide:
    - navigation
---

# Drawbridge

Drawbridge is a self-hosted web manager for a WireGuard VPN server. It gives you a VPN that runs
at home, on a machine you own, so your phone and laptop can reach your home network, and use its
internet connection, from anywhere. Nothing about it depends on a service run by someone else.

You manage it from a web page or from the command line on the host, and a dashboard such as
Homepage can read its status through a read-only API token. There's one admin, and the web page
is reachable only from your home network and the VPN, never from the internet.

## What it does

- **Clients:** add, remove, rename, and pause the phones and laptops that connect. Hand out each
  one's config as a QR code or a file, and give a client new keys when you need to.
- **Connections and traffic:** see who is connected, their session history, and their traffic
  over time, with a log of every change.
- **Server settings:** the address clients connect to, the VPN's own addresses, and the MTU. A
  change that could cut you off is undone after a minute unless you keep it.
- **DNS:** public resolvers by default, or a resolver on the host such as AdGuard Home, which can
  also have your clients' names kept in sync.
- **Care of the host:** a built-in check of the host and the network, encrypted backups,
  optional two-factor authentication, and your own TLS certificate for the web page.

## How it works

Drawbridge installs natively on Debian-family Linux with systemd, as one package for arm64 or
amd64, with no Docker. It supports both IPv4 and IPv6, and it's tested on a Raspberry Pi 5 running
Debian 13.

The VPN doesn't depend on the web page. The tunnel starts at boot from the database, and it keeps
running if the web page stops or is upgraded.

## Where to start

[Getting started](getting-started.md) takes you from a bare host to a phone connected to your VPN.
The guides cover each feature after that.
