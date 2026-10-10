---
title: Troubleshooting
---

Start with the diagnostics. Most problems with the VPN show up there, with a fix:

```bash
sudo drawbridge doctor
```

It is the same as the Diagnostics card on the web UI's System page. See
[Diagnostic tools](diagnostics.md). The services' own logs are
`sudo journalctl -u drawbridge -u drawbridge-tunnel`.

## A client can't connect

| What you see | What to try |
|---|---|
| **No handshake.** `sudo drawbridge client list` shows no recent handshake for the client. | The router's port forward (UDP 51820 to the host) or the endpoint isn't right yet, or the connection has no inbound IPv4 (CGNAT). Check the **Endpoint** and **Tunnel** results in the diagnostics. |
| **It works on mobile data but not on your home Wi-Fi.** | Many routers don't send a connection to their own public address back inside, so a phone at home can't test the port forward. Test from outside, with Wi-Fi off. |
| **A handshake, but nothing loads.** | Another firewall on the host (ufw, firewalld, or Docker, for example) is dropping forwarded traffic, or forwarding is off. The **Host firewall**, **Forwarding sysctls**, and **Uplink** checks say which, and give the fix. |
| **Pages load, but names don't resolve.** | The DNS the VPN hands out isn't answering. See [DNS settings](dns-settings.md), and the **DNS for clients** check. |
| **It stopped working after you changed a setting or rotated a key.** | The device needs the new config: show the QR code or download it again, and import it. See [Clients](client-configuration.md#when-a-config-is-outdated). |

## The web UI

| What you see | What to try |
|---|---|
| **No answer from the web UI.** | It answers only your home network and the VPN. See [Web UI access](web-ui-access.md). |
| **A certificate warning.** | Expected: Drawbridge made the certificate itself. Check its fingerprint with `sudo drawbridge tls show`, or install [your own certificate](tls-certificate.md). |
| **A setting you changed went back.** | A change that could lock you out is undone after a minute unless you press **Keep Changes**, and it's undone if Drawbridge restarts first. See [Server configuration](server-configuration.md#changes-that-could-lock-you-out). |
| **The web UI won't start after you installed an older version.** | An older Drawbridge doesn't open a database that a newer one changed. Install the newer version again, or restore a backup made by the older one. See [Upgrade](upgrade.md). |

## Signing in

| What you see | What to try |
|---|---|
| **You forgot the password.** | `sudo drawbridge admin reset-password` gives the account a new random password. |
| **You lost the phone and the recovery codes.** | `sudo drawbridge admin disable-2fa` turns two-factor authentication off. See [Two-factor authentication](two-factor.md). |
| **Codes are refused, or logins say to wait.** | Wrong passwords and codes are limited: five free tries, then a wait that grows to 15 minutes. The wait ends by itself, and `reset-password` or `disable-2fa` lifts it. A code that is always wrong can mean the host's clock is off; the **Clock** check shows it. |
