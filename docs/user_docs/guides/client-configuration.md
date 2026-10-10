---
title: Clients
---

A client is a phone, laptop, or other device that connects to your VPN. Each one has its own keys
and its own addresses, and a config that holds them.

## Add a client

Open **Clients** in the web UI and press **+ Add Client**, or run this on the host:

```bash
sudo drawbridge client add phone --qr
```

A name is up to 64 letters, digits, spaces, and `.` `_` `'` `-`. Drawbridge gives the client its
own addresses (IPv6 as well as IPv4, when the VPN has IPv6) and its own keys. It can't give a
client a config until the [endpoint is set](server-configuration.md).

## Connect a device

Open the client's page. **Connect a Device** offers the config two ways:

- **Show QR Code:** in the WireGuard app on a phone, tap **+**, then **Scan from QR code**.
- **Download .conf:** import the file in the WireGuard app on a laptop.

The config holds the client's private key, so every time it's shown or downloaded is recorded in
the [log](logs.md). On the host, `client qr NAME` shows the QR code and `client config NAME`
prints the config.

## Pause, rename, and delete

- **Pause** takes the client out of the tunnel, so it can't connect until you **Resume** it. It
  keeps its keys and addresses.
- **Rename** changes only the name. The config and the keys stay the same.
- **Delete** removes the client for good. Its config stops working at once, and its traffic
  history goes with it. It can't be undone.

## When a config is outdated

A config holds the endpoint, port, MTU, DNS servers, and keepalive, the server's public key, and
the client's own keys. If one of them changes after you handed out a config, the client shows as
**outdated**, in the list, on its page, and in the dashboard's Outdated count. Until the device has
the new config it keeps using the old one, which stops working after some changes (a new endpoint
or port, or a rotated key). Show the QR code or download the config again, and import it on the
device, and the flag clears.

## What you can see about a client

- **The Clients page** lists every client with its state. Search by name or address, and use the
  dashboard's tiles to see only the online, paused, or outdated ones. A client is online if it
  completed a handshake in the last three minutes.
- **A client's page** has its last handshake, its endpoint, its traffic, and when it was added and
  last handed its config. Below that are its bandwidth, a list of its connections (**Sessions**),
  its **Activity**, and, with [AdGuard Home](adguard-home.md) connected, its **Recent DNS
  Queries**.
- **The Charts page** (the chart-line icon in the header) draws what was received and sent, and
  the running totals, with a line for each client. One range choice, from 1 minute to 90 days,
  applies to every chart. Traffic is kept for 90 days, and the 1-minute range is live and isn't
  stored.
