---
title: Getting Started
hide:
    - navigation
---

This takes you from a bare Debian-family host to a phone connected to your VPN. The steps:

1. [Get the host and your network ready.](#before-you-start)
2. [Install Drawbridge.](#install)
3. [Set it up in your browser.](#set-it-up-in-your-browser)
4. [Check the host.](#check-the-host)
5. [Connect a first client.](#connect-a-first-client)

## Before you start

Check that you have these:

- **A host that runs Debian or something built on it** (Debian, Raspberry Pi OS, Ubuntu Server),
  with systemd, a kernel that has WireGuard (5.6 or later), and `sudo`. Drawbridge is tested on a
  Raspberry Pi 5 running Debian 13. Other Debian-family systems should work but aren't tested.
- **The host's architecture is arm64 or amd64.** This prints it:

    ```bash
    dpkg --print-architecture
    ```

- **A fixed address on your home network** for the host. Give it a DHCP reservation on your
  router, because the router's port forward points at it.
- **A way in from the internet:** a public IPv4 address, or an IPv6 endpoint. A connection behind
  carrier-grade NAT (CGNAT) or DS-Lite has no inbound IPv4.
- **A name for the endpoint,** such as `vpn.example.com`, with an A record (and an AAAA record for
  IPv6) at your DNS provider. If your public address changes, run a dynamic DNS client to keep the
  record current. Drawbridge doesn't update DNS. You can use the public IP address itself, and
  change to a name later.
- **UDP 51820 forwarded** from your router to the host. Drawbridge uses only two ports: UDP 51820
  for WireGuard and TCP 51821 for the web UI. It doesn't touch the ports other services on the
  host commonly use, such as 53, 80, 443, and 3000.

You can install first and do the router afterward. The VPN won't work from outside until the port
forward is in, and `drawbridge doctor` says what's still missing.

## Install

Drawbridge is a Debian package, `drawbridge_<version>_<arch>.deb`. Each release on GitHub has
one for arm64 and one for amd64, and a `SHA256SUMS` file to check them against.

### Download the package

1. Open the [latest release](https://github.com/stuffam/drawbridge/releases/latest) and download
   the package that matches the host's architecture, and `SHA256SUMS`, to the host. To download
   on another computer, copy the two files over with `scp`.
2. Check the package against the sums, in the folder that has both files:

    ```bash
    sha256sum --check --ignore-missing SHA256SUMS
    ```

    It should print `OK` beside the package's name. If it doesn't, download the package again.

### Install it

```bash
sudo apt install ./drawbridge_<version>_<arch>.deb
```

Keep the `./`: without it, `apt` looks for a package of that name in its repositories. It pulls in
`nftables`, which Drawbridge needs, and `wireguard-tools`, which it recommends (`wg show` is handy
for debugging).

If you'd rather have a script do the download, the check, and the install, every release also has
`install.sh`. Read it, then run it on the host. It asks before it installs anything:

```bash
curl -fsSLO https://github.com/stuffam/drawbridge/releases/latest/download/install.sh
sh install.sh
```

The package's install script runs as root and, on a first install:

- Creates the `drawbridge` system user, and `/etc/drawbridge/secret.key`, the key that encrypts the
  private keys in the database. An upgrade keeps the key it finds.
- Turns on IPv4 and IPv6 forwarding, now and at every boot, and loads the `wireguard` module, now
  and at every boot.
- On a host where the kernel configures IPv6 from router advertisements (a host that uses
  ifupdown), sets `accept_ra` to 2 on the uplink so forwarding doesn't take the host's own IPv6
  address away. It prints a line when it does.
- Tells NetworkManager, when it's running, to leave the `wg0` interface alone.
- Enables and starts both services, then prints where the web UI is and a one-time setup token.

The services run as the `drawbridge` user with only the one privilege they need, `CAP_NET_ADMIN`:

- `drawbridge-tunnel.service` brings the VPN up at boot, from the database.
- `drawbridge.service` is the web UI and the API. If it stops, the VPN keeps running.

The end of the output looks like this:

```text
Setup token: ABCDE-FGHJK-LMNPQ-RSTUV
Open https://vpnhost:51821 from your home network or the VPN, and enter it to create the admin account.
Your browser will warn that the certificate is self-signed. Check that its SHA-256
fingerprint is 3F:9A:...:C1
```

The token and fingerprint are yours alone (the ones above are examples). If you missed them, run
`sudo drawbridge admin setup-token`: it shows them until the admin account exists, and then says
that setup is done. They're in `journalctl -u drawbridge` too.

### Where everything went

| Path | What |
| --- | --- |
| `/usr/bin/drawbridge` | The program: the daemon and the command line. |
| `/usr/lib/systemd/system/drawbridge.service`, `drawbridge-tunnel.service` | The two services. |
| `/etc/drawbridge/secret.key` | The key for the database's secrets. |
| `/var/lib/drawbridge/` | The database, the web UI's TLS certificate (`tls/`), and the saved firewall rules. |
| `/usr/lib/sysctl.d/90-drawbridge.conf` | Turns on forwarding. |
| `/usr/lib/modules-load.d/drawbridge.conf` | Loads `wireguard` at boot. |
| `/usr/lib/NetworkManager/conf.d/90-drawbridge.conf` | Leaves `wg0` unmanaged. |
| `/etc/sysctl.d/91-drawbridge-accept-ra.conf` | Only when the install had to set `accept_ra`. |

The firewall rules are in their own nftables table, `inet drawbridge`. Drawbridge never touches
another table.

## Set it up in your browser

From a computer or phone on your home network, open the address the install printed,
`https://<host>:51821`. If the host's name doesn't resolve on your network, use its LAN address
instead.

Your browser will warn about the certificate, because Drawbridge made it itself. Open the
certificate's details, check that its SHA-256 fingerprint matches the one the install printed, and
continue. You can [serve your own certificate](guides/tls-certificate.md) later to get rid of the
warning.

The web UI answers only your home network and the VPN. A device on another subnet gets no answer,
and so does the internet. To let in another private network, such as a Tailscale tailnet, give its
range to the server. This replaces any extra sources you set before, so list them all, separated
by commas:

```bash
sudo drawbridge server set --admin-allow 100.64.0.0/10
```

Don't put a reverse proxy in front of the UI on the same host. The UI decides who may reach it by
the address a connection comes from, and a proxy on the host forwards every request from the host
itself, which is always allowed, so it would put the UI on the internet. Open it directly instead.

Setup has three steps:

1. **Create the admin account.** Enter the setup token, then choose a username and a password of
   at least 10 characters. Use a password you don't use anywhere else: this account can add a
   device to your network.
2. **Where clients connect.** Enter the endpoint, the name or public IP address your clients
   connect to (for example `vpn.example.com`). You can leave it empty and set it later in
   Settings, but a client can't be given a config until it's set.
3. **DNS for Clients.** A new server hands out Cloudflare's public resolvers (`1.1.1.1` and
   `1.0.0.1`, and the IPv6 pair when the VPN has IPv6), which work anywhere. If the host runs a
   resolver such as AdGuard Home, Pi-hole, or Unbound that answers on the VPN's addresses, setup
   offers it as **This server**. If setup doesn't find yours, it isn't answering on those
   addresses (systemd-resolved, for one, listens only on the host itself), so carry on with the
   public resolvers. Either way, you can change the choice later in Settings.

Then turn on [two-factor authentication](guides/two-factor.md) if you want a second factor at login.

## Check the host

```bash
sudo drawbridge doctor
```

It checks the host and the network, prints a fix for each problem, and changes nothing. The
tunnel, forwarding, the firewall, DNS on the VPN addresses, the endpoint, the clock, and the
certificate are among them. It exits with status 1 if any check failed. The same checks are on the
web UI's System page (the pulse icon in the header), and the dashboard shows a banner when one
warns or fails.

Two results are normal until your network is ready:

- **Endpoint** fails until the endpoint is set and its name resolves from the host. Make the DNS
  record, then press **Run Again** on the System page or run `doctor` again.
- **Clock** warns on a host that keeps time with chrony or ntpd, because the check recognizes only
  `systemd-timesyncd`.

## Connect a first client

Install the WireGuard app on a phone (it's in the App Store and Google Play), or on a laptop
([wireguard.com/install](https://www.wireguard.com/install/)).

Then add the client, in the web UI (**Clients**, then **Add Client**) or on the host:

```bash
sudo drawbridge client add phone --qr
```

The terminal shows a QR code. In the app, tap **+**, then **Scan from QR code**. The code is large,
so if it doesn't fit, make the terminal window bigger or zoom out. The web UI shows the same QR
code, and offers the config as a file for a laptop.

A client's config has the endpoint in it. If you change the endpoint later, a client that was
handed a config shows as **outdated** until it gets the new one.

**Test it from outside.** Turn Wi-Fi off, so the phone uses mobile data, and switch the tunnel on.
A phone on your home Wi-Fi proves nothing about the port forward, because many routers don't send
a connection to their own public address back inside.

```bash
sudo drawbridge client list
```

shows the client as `active` with a handshake a few seconds old. A site such as test-ipv6.com
shows your home network's public address, and your IPv6 address if the VPN has one.

If it doesn't connect:

- **No handshake:** the port forward (UDP 51820 to the host) or the endpoint isn't right yet, or
  the connection has no inbound IPv4 (CGNAT). Run `sudo drawbridge doctor`.
- **A handshake, but nothing loads:** another firewall on the host (ufw, firewalld, or Docker, for
  example) is dropping forwarded traffic, and `doctor` names the command that fixes it.
- **Pages load, but names don't resolve:** the DNS the VPN hands out isn't answering. Check it
  under DNS for Clients in Settings.
- The services' own logs: `journalctl -u drawbridge -u drawbridge-tunnel`.

## What's next

Make a [backup](guides/backup-restore.md) once you've added the clients you want. It's the only
way back if the host's disk fails. To install a newer version later, see
[Upgrade](guides/upgrade.md), and to take Drawbridge off the host, [Remove](guides/remove.md).
