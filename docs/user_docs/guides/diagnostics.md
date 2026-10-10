---
title: Diagnostic Tools
---

Diagnostics check the host and its network for what the VPN depends on: the tunnel, IP
forwarding, the firewall, DNS, and more. Each problem comes with a fix. The checks only read, so
nothing is changed, and it's safe to run them at any time.

The same checks show up in three places:

- **The System page** in the web UI (the pulse icon in the header), in the **Diagnostics** card.
- **The dashboard**, which shows a banner when a check warns or fails.
- **The command line**, with `sudo drawbridge doctor`.

Run them after you install, after you change your router or network, and whenever a client can't
connect.

## On the System page

The Diagnostics card runs the checks when you open the page, and again when you press **Run
Again**. A check can take a few seconds, because it asks DNS. The list stays up while it runs, and
the new results replace it together.

At the top is a summary, such as "All 13 checks passed." or "1 failed, 2 warnings, 10 passed.",
with the time of the run. Under it, each check shows a colored dot, its result, what it found, and,
for a warning or a failure, a **Fix:** line. The result is one of four:

| Result | Meaning |
|---|---|
| Pass | The check found nothing wrong. |
| Warning | Something may be wrong, or is about to be. |
| Failed | Clients or the admin are affected now. |
| Skipped | The check couldn't run, or doesn't apply to this host. It isn't a pass, so the summary names it. |

## On the dashboard

When a check warns or fails, the dashboard shows a banner such as "2 checks need attention". It
lists each check's name and what it found, and links to the System page for the fixes. The banner
is red if any check failed, and amber if they are all warnings.

The dashboard asks for the results when it opens, every five minutes while it's showing, and
whenever the settings change. It leaves out two things it already says itself, from fresher data:
the tunnel check, and an endpoint that isn't set.

## On the command line

```bash
sudo drawbridge doctor
```

It prints each check with its result, what it found, and, for a warning or a failure, the fix,
and then a count. This is an excerpt:

```text
PASS  Tunnel
      wg0 is up with 2 peers.
FAIL  Endpoint
      The server's public address isn't set, so clients can't get a config.
      Fix: sudo drawbridge server set --endpoint vpn.example.com
WARN  Clock
      systemd-timesyncd hasn't reported a synchronized clock. TLS certificates,
      WireGuard handshakes, and two-factor codes depend on the time.
      Fix: sudo timedatectl set-ntp true, then timedatectl status. ...

11 passed, 1 warning, 1 failed, 0 skipped
```

The exit status is 1 if any check failed, and 0 otherwise: warnings don't count. A script can use
that. The checks run inside the daemon, so if `doctor` can't reach it, it says so and exits with
status 1.

## The checks

They run in this order: the tunnel first, then the host under it, then what's around the host.

- **Tunnel.** Fails when the WireGuard interface (`wg0`) is stopped, so no client can connect.
  Start it with `sudo systemctl start drawbridge-tunnel`, and read `journalctl -u
  drawbridge-tunnel` to see why it stopped. It also fails if the interface can't be read at all;
  the daemon's log, `journalctl -u drawbridge`, says why.
- **WireGuard kernel module.** Warns when the `wireguard` module doesn't look loaded. A module
  built into the kernel doesn't show up, so this can be a false alarm. Load it with `sudo modprobe
  wireguard`. The package loads it at every boot.
- **Forwarding sysctls.** Fails when IPv4 forwarding is off, or IPv6 forwarding is off on a VPN
  that uses IPv6, so clients' traffic isn't routed. Turn them on with `sudo sysctl -w
  net.ipv4.ip_forward=1`, and `net.ipv6.conf.all.forwarding=1` for IPv6. The package sets them at
  every boot, so if they turn off again, another file in `/etc/sysctl.d` is overriding it.
- **Uplink.** Fails when the host has no IPv4 default route, so clients' traffic has nowhere to go,
  and warns when the VPN uses IPv6 and the host has no IPv6 default route. Check the host's
  network connection with `ip route show default`, and `ip -6 route show default` for IPv6.
- **Router advertisements.** Fails when the uplink has `accept_ra` set to 1 while IPv6 forwarding
  is on. The kernel then ignores router advertisements, and the host loses its own IPv6 address and
  default route. The package sets `accept_ra` to 2 where it matters, so this is rare. The fix line
  has the exact command. It's skipped when IPv6 is off or the host has no default route.
- **VPN subnets and the LAN.** Fails when a VPN subnet overlaps a subnet on your home network, so
  a client can't tell which network an address is on. The VPN's subnet can't be changed after
  setup yet, so move your home network to another subnet on the router.
- **Drawbridge's firewall table.** Fails when the tunnel is up and Drawbridge's `inet drawbridge`
  nftables table isn't loaded. Clients then aren't NATed, and the web UI isn't restricted to your
  home network and the VPN. The daemon reloads the table within 30 seconds; if it doesn't,
  `journalctl -u drawbridge` says why. It's skipped while the tunnel is stopped, because the table
  goes with it.
- **Host firewall.** Warns when another firewall (ufw, firewalld, Docker, or your own nftables
  rules) drops forwarded traffic and nothing there accepts `wg0`'s. Clients then connect but reach
  nothing. This is a best guess from nftables' rules, and it can't see iptables-legacy. The fix
  depends on the firewall:
    - **ufw:** `sudo ufw route allow in on wg0 && sudo ufw route allow out on wg0`
    - **firewalld:** `sudo firewall-cmd --permanent --zone=trusted --add-interface=wg0 && sudo
      firewall-cmd --reload`
    - **Docker:** `sudo iptables -I DOCKER-USER -i wg0 -j ACCEPT && sudo iptables -I DOCKER-USER -o
      wg0 -j ACCEPT`, and the same with `ip6tables` for IPv6. These rules don't survive a reboot
      until you save them.
    - **Any other firewall:** add a rule that accepts forwarded traffic in and out of `wg0`.
- **DNS for clients.** Tests only when clients are given this server's own VPN addresses as their
  DNS (the **This server** choice in DNS for Clients). It sends a test query to each address, and
  warns when some don't answer and fails when none do. With other servers, such as the default
  public resolvers, it passes without testing them. Make the resolver listen on the VPN addresses
  (for AdGuard Home, `bind_hosts`) and allow the VPN's subnets, or give clients other servers with
  `sudo drawbridge server set --dns 1.1.1.1,1.0.0.1`.
- **Endpoint.** Fails when the server's public address isn't set (`sudo drawbridge server set
  --endpoint vpn.example.com`), or when its name doesn't resolve from the host, so make an A record
  (and an AAAA record for IPv6) at your DNS provider. It warns when the name resolves to a private
  address, which a client on the internet can't reach (unless your network answers it privately on
  purpose; check what a device outside your network gets), and when it resolves to a temporary
  IPv6 address that changes about daily. Point the AAAA record at one of the host's stable
  addresses.
- **Clock.** Warns when `systemd-timesyncd` hasn't reported a synchronized clock. TLS certificates,
  WireGuard handshakes, and two-factor codes depend on the time, and a host without a
  battery-backed clock (a Raspberry Pi, say) needs network time. Turn it on with `sudo timedatectl
  set-ntp true`. Only `systemd-timesyncd` is recognized, so a host that keeps time with chrony or
  ntpd shows this warning even when its clock is right.
- **Free disk space.** Shows the free space where the database is. It warns under 200 MiB and
  fails under 50 MiB, because SQLite can't write when the disk is full and changes are lost. Free
  up space on that filesystem.
- **TLS certificate.** Shows when the web UI's certificate expires. It warns in the last 30 days
  and fails once the certificate has expired. The self-signed certificate renews only when the
  daemon starts, so restart it with `sudo systemctl restart drawbridge`. Nothing renews a
  certificate you installed: install a renewed one, or go back to the self-signed one. See
  [TLS certificate](tls-certificate.md). It's skipped if the web UI has no certificate.
