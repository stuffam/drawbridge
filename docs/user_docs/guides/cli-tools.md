---
title: CLI Tools
---

The `drawbridge` command manages Drawbridge from a terminal on the host. It talks to the running
daemon, the same one behind the web UI, so the two always agree: a client you add on the command
line shows up in the web UI, and the other way around. This page lists the commands. The guides
cover the bigger jobs in full.

## Running commands

- Run the commands as root (`sudo`) or as a member of the `drawbridge` group. They reach the daemon
  through a socket that only those two can open.
- `drawbridge help` lists every command, and `drawbridge <command> -h` lists that command's flags.
- Quote a name that has spaces: `drawbridge client add "Alex's iPhone"`.
- A command that asks before it does something drastic takes `--yes` to skip the question.
- The exit status is 0 when a command worked, 1 when it failed (the daemon refused it or couldn't
  be reached), and 2 when it was used wrong, such as an unknown command or a missing name.
- `--control PATH` names the daemon's socket, for the rare host that runs it somewhere other than
  `/run/drawbridge/control.sock`.

## Clients

| Command | What it does |
|---|---|
| `client list` | Lists the clients with their state (`active`, `paused`, or `not in tunnel`), whether their config is `current` or `outdated`, their addresses, last handshake, endpoint, and the data received and sent. |
| `client add NAME` | Adds a client. A name is 1 to 64 letters, digits, spaces, and `.` `_` `'` `-`. With `--qr`, it shows the new client's QR code. |
| `client show NAME` | Shows one client in full. |
| `client pause NAME` | Takes a client out of the tunnel, so it can't connect until you resume it. |
| `client resume NAME` | Puts a paused client back. |
| `client rename NAME NEW-NAME` | Renames a client. Its config and keys don't change. |
| `client delete NAME` | Deletes a client. It asks first; `--yes` skips that. |
| `client config NAME` | Prints the client's WireGuard config. Save it as a file for a laptop: `sudo drawbridge client config laptop > laptop.conf`. |
| `client qr NAME` | Shows the config as a QR code to scan with the WireGuard app. |
| `client rotate-keys NAME` | Gives the client new keys. The config it holds stops working until it imports the new one. It asks first; `--yes` skips that. |

Printing a config or a QR code counts as handing it out, so the client shows as `current`
afterward. A client whose config has changed since shows as `outdated`.

## Server settings

`server show` prints the settings, and any change that is waiting to be kept. `server set` changes
them. Changes apply to the tunnel right away. A client's config picks up a new endpoint, port, MTU,
DNS, or keepalive when the client gets the config again.

| Flag of `server set` | What it sets |
|---|---|
| `--endpoint HOST[:PORT]` | The public name or address clients connect to, such as `vpn.example.com`. |
| `--port PORT` | The UDP port WireGuard listens on. Change your router's port forward to match. |
| `--mtu N` | The tunnel's MTU, from 1280 to 1500. |
| `--dns LIST` | The DNS servers clients get: comma-separated addresses, `server` (the server's own VPN addresses, if a resolver answers a test query on them), or `none`. With `--force`, `server` uses the VPN addresses even when nothing answers. |
| `--keepalive SECONDS` | How often clients send a keepalive. `0` turns it off. |
| `--client-isolation=false` | Lets clients reach each other. Clients are isolated by default. |
| `--admin-allow LIST` | Extra networks that may reach the web UI, besides your home network and the VPN: comma-separated ranges inside `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `100.64.0.0/10` (Tailscale), or `fc00::/7`, or `none`. It replaces the whole list, so name every range you want. |

Some changes can cut you off from the web UI: changing the listen port, removing a source from
`--admin-allow`, or rotating the server's key. The web UI puts these on probation and undoes them
after a minute unless you keep them. On the command line they apply at once, unless you add
`--safe`:

```bash
sudo drawbridge server set --port 51830 --safe
sudo drawbridge server confirm
```

| Command | What it does |
|---|---|
| `server confirm` | Keeps a change that is waiting, whether it was made with `--safe` or in the web UI. |
| `server revert` | Undoes that change now, without waiting for the minute to pass. |
| `server rotate-key` | Gives the server a new key. Every client stops until it has its new config, so you must hand each one out again. It asks first; `--yes` skips that, and `--safe` undoes it unless you confirm. |
| `apply` | Makes the tunnel and the firewall match the settings now, and lists what it changed. With `--dry-run`, it changes nothing and lists what it would. |

The daemon does what `apply` does every 30 seconds and after every change, so `apply` is for when
you don't want to wait, or after you changed something by hand with `wg` or `nft`. It never starts
a stopped tunnel: use `sudo systemctl start drawbridge-tunnel` for that.

## Checking the host

`sudo drawbridge doctor` checks the host and its network and prints a fix for each problem. It
exits with status 1 if any check failed. See [Diagnostic tools](diagnostics.md).

## The event log

```bash
sudo drawbridge events
```

shows the log, newest first: changes made in the web UI and on the command line, logins, and
drift the daemon corrected. Each line has the time, the event, who did it and from where, the
client it was about, and the details. `--client NAME` shows only the events about one client, and
`--limit N` shows up to N events, from 1 to 500. It shows 50 by default.

## The web UI's TLS certificate

| Command | What it does |
|---|---|
| `tls show` | Shows the certificate the web UI is serving, and its SHA-256 fingerprint. |
| `tls install --cert FILE --key FILE` | Serves your own certificate instead of the self-signed one. |
| `tls reset` | Goes back to the self-signed certificate. |

See [TLS certificate](tls-certificate.md).

## Backups

| Command | What it does |
|---|---|
| `backup create` | Makes an encrypted backup of the database and its key, and asks for a passphrase. `--output FILE` names the file, and `--passphrase-file FILE` reads the passphrase from a file only you can read. |
| `backup restore FILE` | Puts a backup (or one of the host's database snapshots) in place of this host's database. Run it as root with the daemon stopped. |

See [Backup and restore](backup-restore.md).

## The admin account

| Command | What it does |
|---|---|
| `admin setup-token` | Shows the one-time token that first-run setup in the web UI asks for, until the admin account exists. |
| `admin create NAME` | Creates the admin account with a random password, as an alternative to setup in the web UI. |
| `admin reset-password [NAME]` | Gives the admin account a new random password, logs out its sessions, lifts any lockout from failed logins, and revokes its API tokens. |
| `admin disable-2fa [NAME]` | Turns off two-factor authentication, for an admin who lost both the authenticator app and the recovery codes. See [Two-factor authentication](two-factor.md). |

`admin create` and `admin reset-password` print the new password once. Log in, then change it.

## Other commands

- `drawbridge version` prints the version.
- `drawbridge serve` and `drawbridge tunnel up|down` are what the two services run. Start and stop
  them with `systemctl`, not by hand.
