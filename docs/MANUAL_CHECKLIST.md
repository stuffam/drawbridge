# Manual checklist: real hardware

This is the authoritative record of what has actually run on real hardware, and what hasn't.
Development usually happens away from a real host (CLAUDE.md, "Usually no real host here"), so
anything only tested in CI or a container belongs here as unverified until someone runs it on a
real one.

Results marked verified ran on the reference platform (docs/PLAN.md §2): a Raspberry Pi 5
running Debian 13 (arm64), with NetworkManager managing the network, AdGuard Home on the host as
the clients' resolver, and a consumer router. On a different setup, check docs/REQUIREMENTS.md
first: some steps (IPv6 on an ifupdown host) need a workaround there.

The tags matter, so keep them current when you touch the code a step describes:

- `[UNVERIFIED]`: expected to work, but not yet run on real hardware.
- `[VERIFIED YYYY-MM-DD]`: run on real hardware, and the result matched. Note anything surprising.
- `[FAILED YYYY-MM-DD]`: run on real hardware, and the result didn't match. Say what happened.
- `[NEXT]`: can't be run yet, because something it depends on doesn't exist yet.

## 0. Preparing a host and the router

These are the preparation steps from docs/PLAN.md §14, M0.

- `[VERIFIED 2026-09-27]` The host has a reserved LAN IPv4 address.
- `[VERIFIED 2026-09-27]` The host has a stable IPv6 address:
  `ip -6 addr show scope global -temporary` lists the uplink's SLAAC address (a global address,
  the one that matters for an IPv6 endpoint) and possibly a ULA (`fc00::/7`, not
  internet-routable). A dynamic DNS client that tracks the global address in the AAAA record
  covers a short RA lifetime, so it needs no separate handling.
- `[VERIFIED 2026-09-27]` UDP 51820 (IPv4) is forwarded to the host.
- `[VERIFIED 2026-09-27]` The FQDN has an A and an AAAA record, both kept current by a dynamic DNS
  client in case the host's addresses change.
- `[VERIFIED 2026-09-27]` The router blocks unsolicited inbound IPv6.

## 1. Installing a build

[The install guide](install.md) is the admin's version of these steps; this is the record of what
ran on real hardware.

Get the package from CI: open the latest run of the **CI** workflow for the branch, download the
`drawbridge-deb` artifact, unzip it, and copy `drawbridge_<version>_<arch>.deb` to the host. The
package only exists when every CI job passed, including the kernel tests.

- `[VERIFIED 2026-09-26]` `sudo apt install ./drawbridge_<version>_arm64.deb` succeeds. On a first
  install it prints that it created the `drawbridge` user, then the setup token, the web UI's
  address (`https://<hostname>:51821`), and the certificate's SHA-256 fingerprint (M2).
- `[VERIFIED 2026-09-26]` `sudo ls -l /etc/drawbridge/secret.key` shows 32 bytes, `root drawbridge`,
  `-rw-r-----`.
- `[VERIFIED 2026-09-26]` `systemctl status drawbridge-tunnel` shows `active (exited)`, and
  `systemctl status drawbridge` shows `active (running)`.
- `[VERIFIED 2026-09-26]` `curl -sk https://127.0.0.1:51821/healthz` prints `{"status":"ok"}`, and
  `ss -ltn | grep 51821` shows `*:51821` (every address; M2). §5 checks who can reach it.
- `[UNVERIFIED]` The page loads through an SSH tunnel from a laptop:
  `ssh -L 51821:127.0.0.1:51821 <user>@<host>`, then open `https://localhost:51821` and accept the
  certificate. It shows "Server v… (commit …) is running."
- `[VERIFIED 2026-09-26]` `systemd-analyze security drawbridge drawbridge-tunnel` rates both units
  about 1.8 ("OK"), matching the offline scores from 2026-09-26. Both scored 1.8.
- `[VERIFIED 2026-09-26]` `journalctl -u drawbridge -u drawbridge-tunnel` shows "tunnel up" and
  "Drawbridge started", with journald's timestamps only, and (M2) the certificate's fingerprint
  and, until the admin account exists, the setup token.
- `[VERIFIED 2026-09-26]` Other services on the host keep working with Drawbridge installed: a DNS
  resolver (AdGuard Home, ports 53 and 3000) and a rootless container publishing ports 80 and 443.
  Checked by the listening sockets and a DNS lookup through the resolver; the container's apps
  themselves weren't exercised.

## 2. The VPN (M1's exit criteria)

The host side:

- `[VERIFIED 2026-09-26]` `ip -d addr show wg0` shows a WireGuard interface with `10.8.0.1/24` and
  an `fd…::1/64` address, MTU 1420.
- `[VERIFIED 2026-09-26]` `sudo nft list table inet drawbridge` shows the masquerade and MSS rules,
  and `sudo cat /var/lib/drawbridge/nftables.conf` holds the same ruleset. If `tunnel up` failed
  with "Operation not supported", an nftables module didn't load: try `sudo modprobe -a
  nft_chain_nat nft_masq`, then `sudo systemctl restart drawbridge-tunnel`, and note the result
  here. (The ruleset's other expressions, `rt mtu` included, are part of `nf_tables` itself.) The
  saved file and the live table carried the same revision, and no module failed to load.
- `[VERIFIED 2026-09-26]` `sysctl net.ipv4.ip_forward net.ipv6.conf.all.forwarding` prints 1 for
  both, and the host still has its own global IPv6 address. That's because NetworkManager handles
  Router Advertisements itself (`accept_ra` is 0 on its uplink, so the installer leaves it
  alone). `[UNVERIFIED]` on a real ifupdown host, where the installer should set `accept_ra=2`
  on the uplink and keep its IPv6; the kernel integration tests cover the effect in namespaces.
- `[VERIFIED 2026-09-26]` `nmcli device status` lists `wg0` as `unmanaged`.

A phone, on mobile data with Wi-Fi off:

- `[VERIFIED 2026-09-27]` `sudo drawbridge server set --endpoint <your FQDN>`, then
  `sudo drawbridge client add phone --qr`, shows a QR code that the WireGuard app scans (tap +,
  then "Scan from QR code"). It scanned fine, but the terminal QR code barely fits a typical
  terminal window; a small window may need to be enlarged or zoomed out first.
- `[VERIFIED 2026-09-27]` With the tunnel on, `sudo drawbridge client list` shows `phone` as
  `active`, with a handshake seconds ago and the carrier's address as its endpoint.
- `[VERIFIED 2026-09-27]` test-ipv6.com on the phone shows working IPv4 and IPv6, with the home
  network's public IPv4 address. It also notes that the browser "has a real working IPv6
  address - but is avoiding using it": expected with NAT66, since operating systems prefer IPv4
  over a ULA source (docs/PLAN.md §5.2).
- `[VERIFIED 2026-09-27]` Ads are blocked, and the resolver's query log (AdGuard Home's) shows
  queries from `10.8.0.2`.
- `[VERIFIED 2026-09-28]` Over the IPv6 endpoint: with a router firewall rule that lets UDP 51820
  in over IPv6 to the host's stable address, `sudo drawbridge server set --endpoint <that
  address>`, then deleting the phone's tunnel and reconnecting from a fresh QR code,
  `sudo drawbridge client show` showed an IPv6 endpoint (the phone's carrier-assigned address)
  with real traffic flowing. The endpoint was set back to the FQDN afterward.
- `[VERIFIED 2026-09-27]` `sudo drawbridge client pause phone` stops the phone's browsing within
  seconds, and `client list` shows it `paused`. The peer disappeared from `wg show` the instant
  the command returned, and the phone had no internet right after.
- `[VERIFIED 2026-09-27]` `sudo drawbridge client resume phone`: the phone reconnects on its own,
  in about 2 seconds (well under the ~15 s this expected), confirmed by the WireGuard app's own
  log (`Received handshake response` right after resume) and matched by `wg show` afterward. A
  polling check on the server side missed the exact moment (its own startup lag, and the phone
  briefly switching between Wi-Fi and cellular a bit later per its log); the client-side log was
  the more reliable source here.
- `[VERIFIED 2026-09-27]` `sudo systemctl restart nftables` (whose ruleset flushes every table):
  `sudo nft list table inet drawbridge` failed with "No such file or directory" right after and
  at +10 s, then worked again by +25 s. `journalctl -u drawbridge` logged `corrected drift
  changes="[applied nftables revision …]"` about 20 s after the restart.
- `[VERIFIED 2026-09-27]` `sudo systemctl stop drawbridge-tunnel` removes `wg0` (`ip link show
  wg0` then says "does not exist") while `drawbridge.service` stays `active`. `sudo systemctl
  start drawbridge-tunnel` brings it back with all peers reconfigured from the database, and the
  connected client reconnected on its own within about 20 s of the interface returning.
- `[VERIFIED 2026-09-27]` After a reboot, `drawbridge.service` is `active (running)` and
  `drawbridge-tunnel.service` is `active (exited)` (expected for its `Type=oneshot,
  RemainAfterExit=yes`) with no manual step. `wg0` came back with the phone's peer already
  configured from the database, and the phone reconnected on its own. No errors in
  `journalctl`, the `inet drawbridge` nftables table was present (rebuilt after
  `nftables.service`, per the unit's ordering comment), both forwarding sysctls read `1`,
  NetworkManager still showed `wg0` unmanaged, and the control socket was back with the
  right permissions.
- `[VERIFIED 2026-09-27]` Installing a newer `.deb` over this one leaves the tunnel up. With a
  client actively connected (handshake 45s old), a reinstall left `wg0` on the same interface
  index and the tunnel unit untouched; only `drawbridge.service` stopped and restarted (about a
  second in the journal), and the client's session never dropped — the next handshake came
  16s later with the same `client_sessions` row still open, not a fresh one. The TLS
  certificate's fingerprint was unchanged.
- `[VERIFIED 2026-09-26]` The same reinstall upgraded the database from schema 2 to 3 and kept its
  settings, so an existing install picks up a new migration.

## 3. Removing

- `[VERIFIED 2026-09-27]` `sudo apt remove drawbridge` stops both units (`systemctl status`
  then says "could not be found"), removes `wg0` and the `inet drawbridge` table, and leaves
  `/var/lib/drawbridge` (the database, `nftables.conf`, and `tls/`) and `/etc/drawbridge`
  (`secret.key`). Reinstalling the same `.deb` afterward brought both units, `wg0`, and the
  nftables table straight back with every existing client and setting intact (no fresh setup
  token or new admin account needed), and the same TLS certificate fingerprint as before.
- `[UNVERIFIED]` `sudo apt purge drawbridge` deletes `/var/lib/drawbridge` and `/etc/drawbridge`
  (including the secret key), and keeps the `drawbridge` user.

The scripts behind these steps changed on 2026-10-04 (`remove` no longer disables the units, and
a reinstall goes by their enabled state): §19 runs the removal and reinstall steps again.

## 4. A self-hosted CI runner

CI's "Integration (kernel WireGuard)" job runs on GitHub's `ubuntu-24.04` runner, and should
stay there while the repository is public: a self-hosted runner attached to a public repository
runs code from forks' pull requests. On a private fork, a self-hosted arm64 runner (for example,
a rootless Docker container on a spare host) could take the job by changing its `runs-on` to
`[self-hosted, linux, arm64]` once it has everything below. It needs:

- Registration with the repository, with the default labels (`self-hosted`, `Linux`, `ARM64`).
- Root inside the container, or passwordless `sudo`.
- `--privileged` on the container, for CAP_NET_ADMIN and CAP_SYS_ADMIN (to create network
  namespaces). In rootless Docker, those capabilities apply only inside the container's user
  namespace, so the tests still can't change the host's own network. Without it, the job's
  preflight shows `CapEff: 00000000a80425fb`, Docker's default set.
- `ip` (iproute2) and `nft` (nftables), plus `git`, `curl`, and `tar` (setup-go downloads Go).
  On an Ubuntu or Debian image: `apt-get install -y --no-install-recommends iproute2 nftables`.
- These kernel modules loaded on the host, since a rootless container can't load them:
  `sudo modprobe -a wireguard veth nf_tables nft_chain_nat nft_masq`. To keep them loaded across
  reboots, list them in `/etc/modules-load.d/drawbridge-ci.conf`. The preflight tries each
  feature in a throwaway network namespace rather than looking for module names, since
  `nft_rt` and `nft_exthdr`, for example, are part of `nf_tables`.

The job's preflight step, `test/integration/preflight.sh`, prints what the runner has and lists
everything it's missing at once. `make test-integration` runs the same check locally.

- `[FAILED 2026-09-26]` A first attempt: root, IPv6, and nft were there, but `ip` was missing and
  the container wasn't `--privileged`, so the job moved to GitHub's runners.
- `[NEXT]` The Integration job passes on such a runner.
- `[NEXT]` A run leaves the host's own network untouched: afterward, `ip link` and
  `sudo nft list tables` on the host show nothing new.
- `[VERIFIED 2026-10-05]` The kernel tests (`make test-integration`'s command, minus the two
  journald tests, which write test entries into the host's real journal) pass on the reference
  platform itself: every test, in 86.7 s at the lowest priority on Linux 6.18, with the real tunnel
  up beside them. They left the host's own network as it was: no namespace afterward, and the same
  interfaces, `nft list tables`, `wg0` with its peers, sysctls, listeners, and files in
  `/var/lib/drawbridge`. That isn't the `[NEXT]` run above (a self-hosted runner), only the same
  tests on this machine.

## 5. The admin API (M2)

These check the API directly with `curl` from a laptop on the home network; §6 covers the same
ground through the web UI. Replace `<host>` with the host's name or LAN address.

Reachability:

- `[VERIFIED 2026-09-26]` `sudo nft list table inet drawbridge` has `set admin_allowed4` with the
  LAN's IPv4 subnet (the router's, such as `192.168.4.0/22`) and `set admin_allowed6` with the
  LAN's IPv6 /64, plus the VPN subnets, loopback, and link-local, and an `input` chain that drops
  TCP 51821 from everything else.
- `[VERIFIED 2026-09-26]` From a laptop on the LAN, `https://<host>:51821` loads after the
  certificate warning, and the browser's certificate details show the fingerprint the install
  printed.
- `[VERIFIED 2026-09-27]` From the phone on mobile data with the VPN on,
  `https://10.8.0.1:51821/healthz` answers with `{"status":"ok"}`.
- `[VERIFIED 2026-09-27]` From the phone on mobile data with the VPN off, `https://[<the host's
  public IPv6 address>]:51821` times out (the firewall drops it; the app would answer 403). Also
  try the public IPv4 address, which the router shouldn't forward at all.
- `[VERIFIED 2026-09-27]` A container can't reach the host's loopback: `docker run --rm
  curlimages/curl -skm 3 https://10.0.2.2:51821/healthz` fails (curl exit 7, couldn't connect).
  If it answers, rootless Docker's host loopback is on, and a container could reach the admin UI
  (ADR 0011).
- `[VERIFIED 2026-09-26]` The extra admin sources: from the host's own Tailscale address (`curl
  -sk https://<its 100.x address>:51821/healthz`), the answer is 403 until `sudo drawbridge server
  set --admin-allow <the tailnet's prefix>`, then 200, and `admin_allowed4` lists the prefix. A
  public range such as `203.0.113.0/24` is refused. This checks the app's layer only: the request
  arrives on loopback, which the firewall always accepts.
- `[UNVERIFIED]` From another device on the tailnet, `https://<the host's 100.x address>:51821`
  loads with the prefix set, and times out (the firewall drops it) after `--admin-allow none`.

Setup and a session, from a laptop on the LAN (`-k` because the certificate is self-signed;
compare its fingerprint first):

```bash
H='X-Drawbridge: 1'; J='Content-Type: application/json'
curl -sk https://<host>:51821/api/setup                       # {"needed":true}
curl -sk -c jar -H "$H" -H "$J" https://<host>:51821/api/setup \
  -d '{"token":"<from the install>","username":"admin","password":"<10+ characters>"}'
curl -sk -b jar https://<host>:51821/api/clients              # the clients, as JSON
curl -sk -b jar -H "$H" -H "$J" https://<host>:51821/api/clients -d '{"name":"laptop"}'
curl -sk -b jar -OJ https://<host>:51821/api/clients/<id>/config   # saves laptop.conf
```

- `[UNVERIFIED]` The commands above work end to end (setup, login, adding a client, downloading
  its config) from an actual laptop on the LAN.
- `[VERIFIED 2026-09-27]` A change without the `X-Drawbridge` header is refused with 403 before
  the session is even checked (`POST /api/clients` with no session and no header: 403; the same
  request with the header but still no session: 401). Checked directly against the running
  service, not the walkthrough above.
- `[VERIFIED 2026-09-27]` `sudo drawbridge events` lists `auth.setup_completed`, `client.added`,
  and `client.config_viewed` by the admin from the laptop's LAN address, and the CLI's own
  changes as `root (cli)` (seen on a `client.paused`).
- `[VERIFIED 2026-09-27]` Six wrong passwords in a row (a nonexistent username, so the real
  account was never touched) made the seventh wait: `429` with `Retry-After: 2` and a matching
  error message.
- `[VERIFIED 2026-09-27]` `sudo drawbridge admin reset-password` prints a new password that works
  at once: `POST /api/auth/login` with it returned 200 and a session immediately. (It replaces
  the current password, so set a memorable one afterward.)
- `[VERIFIED 2026-09-27]` After `sudo systemctl restart drawbridge`, the session cookie from
  before the restart still worked (`GET /api/clients` returned 200 with the same cookie jar), and
  the certificate's SHA-256 fingerprint was identical before and after.

## 6. The web UI (M3)

M3's exit criteria: everything can be done from a phone or a desktop browser, installed from the
`.deb` on a real host.

- `[VERIFIED 2026-09-26]` On a laptop on the LAN, `https://<host>:51821` (after the certificate
  warning) goes to setup. The token from the install creates the account, and the second step
  sets the public address.
- `[VERIFIED 2026-09-26]` Clients → Add "phone": the QR code shows at once, and the WireGuard app
  on the phone imports it and connects (mobile data, Wi-Fi off). Within 5 seconds the client
  list shows it online, with its endpoint and traffic.
- `[VERIFIED 2026-09-27]` "Download .conf" saves `phone.conf`, which the WireGuard app on a laptop
  imports.
- `[VERIFIED 2026-09-27]` Pause in the list stops the phone's browsing; Resume brings it back
  within about 15 seconds.
- `[VERIFIED 2026-09-27]` Settings → MTU 1380 → Save: `ip link show wg0` shows mtu 1380 at once,
  and Logs shows "Changed server settings" with "mtu: 1420 → 1380". Set it back.
- `[VERIFIED 2026-09-27]` Settings → DNS "This server": a phone that downloads its config again
  gets the server's VPN addresses, and the resolver's query log (AdGuard Home's) shows its
  queries. This needs a resolver listening on the VPN addresses (docs/REQUIREMENTS.md).
- `[UNVERIFIED]` First-run setup's DNS step, on a fresh install: with AdGuard Home listening on
  all addresses, the check reports both VPN addresses answering and preselects *This server*.
  With no resolver on the host, it reports nothing listening and preselects the public resolvers.
  The unit and browser tests cover both outcomes with a stand-in resolver and the fake backend;
  a real resolver on the host hasn't been asked.
- `[VERIFIED 2026-10-05]` `sudo drawbridge server set --dns server` on a host with AdGuard Home
  listening on all addresses saves both VPN addresses. It printed "DNS check: 10.8.0.1: A resolver
  answered." and the same for the IPv6 address, and left the setting as both addresses (they were
  already saved, so nothing changed and nothing was logged). `--force` did the same.
- `[UNVERIFIED]` With the resolver stopped, `server set --dns server` refuses and says nothing
  answers, and `--force` saves them anyway. The tests cover both with a stand-in resolver. (Trying
  it means stopping the host's resolver, which the home network's DNS depends on, so it was left.)
- `[VERIFIED 2026-09-27]` The same pages work on the phone itself, through the VPN at
  `https://10.8.0.1:51821`, and are usable at phone width.
- `[VERIFIED 2026-09-27]` Account → the laptop's and the phone's sessions are listed; logging the
  phone out from the laptop sends the phone to the login on its next action.
- `[VERIFIED 2026-09-27]` After an hour with the tab closed or in the background, the next action
  goes to the login. (A visible page polled every 5 seconds then, which counted as use; now its
  live stream checks the session as often. See the step in §7.)

- `[UNVERIFIED]` On the dashboard, the Bandwidth, Server, and Clients boxes outline in blue under
  the pointer, like the counts at the top, and a click on one (not on a link, button, or control in
  it) opens the Charts page, the server settings, and the client list. A tap does on the phone. The
  boxes have no "Settings" or "All Clients" link now. A headless browser has done all of it.

## 7. Session tracking (an M4 slice, built ahead of the rest of it)

`Service.TrackConnections` (docs/PLAN.md §6.4) polls every 5 s and records `client.connected`,
`client.disconnected`, and `client.roamed`, with a session's own bytes exposed alongside each
client's all-time totals. A session closes when a client's peer leaves the tunnel outright
(paused, deleted, the tunnel down) or goes idle for longer than the existing Online/Idle
threshold (3 minutes) — WireGuard has no disconnect signal, so a real device going quiet is the
only sign a session actually ended.

- `[VERIFIED 2026-09-27]` Two real clients' first handshakes each produced a `client.connected`
  event (`sudo drawbridge events`), and roaming (a client's endpoint changing, such as switching
  networks) produced `client.roamed` without ending the session.
- `[VERIFIED 2026-09-27]` A client left idle past 3 minutes produced `client.disconnected` with
  the session's duration and bytes (observed: `duration: 3m0s` and `duration: 3m35s`, both at the
  threshold, not early or late by more than the 5 s poll), and `drawbridge client show` stopped
  showing its "Connected"/"Received (session)"/"Sent (session)" rows until its next handshake.
- `[VERIFIED 2026-09-27]` Manually disconnecting and reconnecting a real client resets "This
  session" on the dashboard to zero, rather than continuing to add to it. This was the actual
  bug that prompted the idle-based closing rule above; before the fix, a reconnect was
  indistinguishable from an endpoint change (it only produced `client.roamed`).
- `[VERIFIED 2026-09-27]` Resuming a paused client resets the peer's all-time "Total" bytes to
  zero (WireGuard resets a peer's counters when it's re-added): observed 27.4 MiB received /
  270.8 MiB sent drop to 0 B / 0 B immediately after `drawbridge client resume`.
- `[VERIFIED 2026-09-27]` Pausing a client with a currently *open* session (a recent handshake,
  not already idled out) closes that session immediately, rather than waiting for the idle
  timeout: pausing a client connected 28m ago (last handshake 18s prior) produced
  `client.disconnected` with `duration: 28m31s` at the same timestamp as `client.paused`, not
  the 3-minute idle wait. On 2026-10-05, with a throwaway client connected from a network namespace
  over the loopback (§13 describes it), `client.disconnected` came 4 s after `client.paused`, on
  the next 5 s poll, so the close happens within one poll and isn't always in the same second.
  Rotating that client's keys closed the session in the same second, and after `client resume` its
  next handshake came about 13 s later (WireGuard's own retry), as a new session.
- `[UNVERIFIED]` The Logs page's "Client connections" filter, clicked through a real browser,
  shows the same connect/disconnect/roam events `drawbridge events` does. (Needs a real
  logged-in browser session; see §5.)
- `[VERIFIED 2026-10-05]` Under the installed units' sandbox (`ProtectSystem=strict`, only
  `CAP_NET_ADMIN`), the daemon's events reach the journal as fields: `sudo journalctl -u drawbridge
  DRAWBRIDGE_CLIENT=<a client's name>` lists that client's events (added, paused, connected,
  disconnected), `-o json` shows `DRAWBRIDGE_EVENT`, `DRAWBRIDGE_CATEGORY`, `DRAWBRIDGE_VIA`, and
  `PRIORITY`, and a wrong password at the login shows under `journalctl -u drawbridge -p warning`.
  Real journald accepts the entries (the integration tests check that against the runner's
  journald, with a fake backend), and the unit's sandbox has now been run against it. All of it was
  seen: a throwaway client's `client.added`, `client.paused`, `client.resumed`, `client.renamed`,
  `client.deleted`, `client.connected`, and `client.disconnected` matched by `DRAWBRIDGE_CLIENT`
  (and a real client's roamed, connected, and disconnected events from earlier days), with
  `PRIORITY` 6 and the category, the way (`cli` or `system`), and the actor as fields. One wrong
  password for a made-up username (a 401, from the loopback) was logged at `PRIORITY` 4 as
  `auth.login_failed` with `DRAWBRIDGE_ACTOR` the typed name, `DRAWBRIDGE_VIA=web`, and
  `DRAWBRIDGE_SOURCE_IP`. The password is nowhere in the journal or the event log.
- `[UNVERIFIED]` The Logs page's Event, Client, and When filters narrow the list as they say, and
  Export CSV saves `drawbridge-events.csv` with a header row and one row per matching event
  (not just the 50 shown), in a spreadsheet: times in UTC, details as JSON, and a failed login
  whose name was typed as `=1+1` shown as that text, not as 2. The filters and the file are
  tested against a fake backend, and a headless browser has downloaded it.
- `[VERIFIED 2026-09-27]` The dashboard's "All Clients" line matches the sum of the
  individual clients' totals: two real clients at 0 and 172,312/137,524 bytes showed
  `↓ 172 KB · ↑ 138 KB`. The host had no browser libraries installed, and a container can't
  reach the host's admin UI (the same isolation §5 checks), so the number was read from a
  browser on another machine on the LAN.
- `[UNVERIFIED]` The daemon's writes over a day of real use are within the budget (docs/PLAN.md
  §6.4). Read the bytes it has sent to storage twice, 24 hours apart, with `sudo grep write_bytes
  /proc/$(systemctl show -p MainPID --value drawbridge)/io`: the difference should be less than
  about 190 MiB for a household of ten devices, which is the budget's 94 MiB of database log and
  at most as much again for checkpoints. (The test measures the log of a simulated day against
  a real database; the real daemon's number includes the checkpoints, the nftables copy it saves
  on each apply, and anything else it writes.)
- `[VERIFIED 2026-10-05]` The live stream (`GET /api/stream`) on the reference platform, with the
  dashboard open on a laptop: a real phone turning its tunnel on shows as Online within about 6
  seconds with no touch of the page, `sudo drawbridge client pause NAME` on the host changes the
  tiles within a second, and the Logs page lists a `client.connected` row at the top as it
  happens. The stream and its fallback are tested against a fake backend in a headless browser.
  Run with the maintainer's laptop (through the reverse proxy) watching, and a throwaway client
  connected from a network namespace (§13 describes it) standing in for the phone. A client added
  with `client add` appeared in the client list by itself (before it was mentioned). When it
  connected it showed Online in under 6 s with no touch (the daemon logged the session 2.4 s
  after the connect), and the Logs page had the `client.connected` row at the top as it happened.
  `client pause` changed the tiles within about a second.
- `[UNVERIFIED]` The stream through the real network and the real daemon: a dashboard left
  visible stays current overnight and stays logged in until twelve hours after login, while a
  hidden tab idles out after an hour.
- `[VERIFIED 2026-10-05]` `sudo systemctl restart drawbridge` with a page open brings the page back
  to live by itself within seconds, and `sudo systemctl stop drawbridge` with a page open returns
  at once, not after ten seconds. With the dashboard open through the reverse proxy, a restart
  brought the page back live within a few seconds with no reload, and the connected throwaway
  stayed Online. `stop` returned in 0.03 to 0.04 s with a page's stream open (0.02 s with none),
  and after a `start` the web UI's `/healthz` answered 0.11 s later and the control socket 0.12 s
  later. While the daemon is away, the dashboard shows a red alert at the top of the page. A 10 s
  stop went unnoticed because the page was scrolled down; in a 45 s stop with a second window open
  directly on port 51821 as well, the alert showed in both windows, and the reverse proxy's log
  has the page's polls answered 502 every 5 s throughout.
- `[UNVERIFIED]` A connected client's session bytes on the dashboard still grow at every 5 second
  refresh although the database gets them once a minute. (The daemon's session counters were seen
  advancing on its 5 second poll; the dashboard, and the database's once-a-minute write, weren't
  looked at.)
- `[VERIFIED 2026-10-05]` After `sudo systemctl restart drawbridge` the session carries on (no new
  `connected` event) and its bytes are right again at the first poll. With a throwaway client
  connected from a network namespace over the loopback (§13 describes it) and pinged five times a
  second, `systemctl restart drawbridge` (the journal has "Drawbridge stopping" and "Drawbridge
  started" 0.1 s apart) left the session's start time unchanged, and the event log still had one
  `client.connected` and no `client.disconnected` for it. The session's byte counters carried on
  rather than resetting (15,008 bytes before, 23,584 a few seconds after). After a `systemctl stop`
  and `start` with the traffic quiet, the baseline between the kernel's totals and the session's
  counters (the kernel's total minus the session's bytes: 308 received, 220 sent) was exactly what
  it was before, and the session's counters matched the kernel's after the next poll. The pings
  never dropped: 342 of 342 answered, with at most 0.21 s between two, straight through the
  restart.

## 8. Diagnostics: `drawbridge doctor` (the first M5 slice)

`drawbridge doctor` asks the daemon (`GET /v1/diagnostics` on the control socket) to run the 13
checks of docs/PLAN.md §6.6. The daemon reads the host: the kernel's sysctls, its routing table,
`nft -j list ruleset`, the resolver, and the disk.

- `[VERIFIED 2026-09-29]` The checks work inside the daemon's sandbox. A transient `systemd-run`
  unit with `drawbridge.service`'s hardening properties (`User=drawbridge`, only
  `CAP_NET_ADMIN`, `ProtectSystem=strict`, `ProtectKernelTunables`, `ProtectProc=invisible`, the
  address-family and system-call filters) ran `serve --backend fake` with a scratch state
  directory. Every check read what it needed: on the reference platform it printed 12 passes and
  one failure, the endpoint, which a fresh database hasn't set.
- `[VERIFIED 2026-09-29]` Against the real kernel in network namespaces
  (`test/integration/doctor_test.go`): `accept_ra` 1 on the uplink fails and 2 passes, forwarding
  off fails for IPv4 and for IPv6, another table's forward chain with `policy drop` warns until a
  rule accepts `wg0` (and stays warning for a rule that accepts only one destination port), and
  `tunnel down` fails the tunnel check.
- `[VERIFIED 2026-10-05]` `sudo drawbridge doctor` against the installed daemon on the reference
  platform (the sandboxed run above used the fake backend, so it didn't see the real `wg0`). All 13
  checks passed, first on the build that was installed (`afff457`) and again after the in-place
  upgrade to `17e7932`: `wg0` up with its peers, the WireGuard module, both forwarding sysctls, the
  uplink for IPv4 and IPv6, `accept_ra` 0 under NetworkManager, the VPN's subnets against the LAN's,
  the table loaded, no other firewall dropping forwarded traffic, both VPN addresses answering DNS,
  the endpoint's A and AAAA records, `systemd-timesyncd` synchronized, free disk space, and the
  admin's own certificate.
- `[UNVERIFIED]` The System page (the pulse icon in the header) lists the same 13 checks, in the
  same order, with the same results as `sudo drawbridge doctor`, and **Run again** gives a fresh
  set. With nobody connected, `sudo sysctl -w net.ipv4.ip_forward=0` makes the next run show
  Forwarding sysctls as Failed, with the command that turns it back on; running that command
  clears it on the run after. (The command-line half was run on 2026-10-05; see the next item.)
- `[VERIFIED 2026-10-05]` With nobody connected, `sudo sysctl -w net.ipv4.ip_forward=0` makes the
  next `sudo drawbridge doctor` show Forwarding sysctls as Failed, with the command that turns it
  back on, and running that command clears it on the run after. `doctor` printed "FAIL
  Forwarding sysctls: IPv4 forwarding is off, so VPN clients' traffic isn't routed. Fix: sudo
  sysctl -w net.ipv4.ip_forward=1. The package sets them at boot in
  /usr/lib/sysctl.d/90-drawbridge.conf; if they turn off again, another file in /etc/sysctl.d is
  overriding it", with 12 passed and 1 failed, and exited 1. The command it printed, run as
  printed, put forwarding back (it had been off for about 0.1 s), and the next run was 13 passed
  and exit 0. With the tunnel stopped, `doctor` also fails DNS for clients (nothing answers on the
  VPN addresses once `wg0` is gone), so the dashboard's banner would name that check while its own
  "tunnel is stopped" banner is up (10 passed, 2 failed, 1 skipped).
- `[UNVERIFIED]` On a host with ufw (default forward policy `DROP`), firewalld, or rootful Docker,
  the host firewall check warns, and the printed command (`ufw route allow`, the trusted zone,
  `DOCKER-USER`) clears it. The parser is tested on nft output shaped like each tool's, not on a
  real install of each.
- `[UNVERIFIED]` On an ifupdown host with `accept_ra` 1, the accept-ra check fails with the fix in
  its hint, and the fix clears it.
- `[UNVERIFIED]` An endpoint name whose AAAA record is a temporary address warns.
- `[UNVERIFIED]` A host that keeps time with chrony or ntpd shows the clock warning even when the
  clock is right (a known limit, docs/REQUIREMENTS.md).
- `[VERIFIED 2026-10-05]` With nobody connected, `sudo sysctl -w net.ipv4.ip_forward=0` makes the
  dashboard's banner name Forwarding sysctls (at once when the page is reloaded), and turning it
  back on clears the banner the same way. Forwarding was off for 60 s, `doctor` read 12 passed and
  1 failed, and on the maintainer's dashboard a reload showed the banner naming Forwarding
  sysctls; after forwarding was back on, a reload cleared it. The five-minute timer, and a save as
  the trigger, weren't tried.
- `[VERIFIED 2026-10-05]` With the tunnel stopped, the dashboard's own "tunnel is stopped" banner
  appears and the diagnostics banner doesn't name the Tunnel check a second time. The tunnel was
  stopped for 60 s. On the maintainer's phone (a screenshot of the dashboard) there were two red
  boxes: "The tunnel is stopped, so no client can connect. Changes are saved and
  apply when it starts: sudo systemctl start drawbridge-tunnel", and, apart from it, "1 check
  needs attention", listing only "DNS for clients" (which `doctor` fails while `wg0` is gone, along
  with Tunnel) with a "See System" link. The Tunnel card read "Stopped" in red, and the banners
  cleared after the start. One wart: that check's detail repeats "Nothing answered within two
  seconds." once for each address (IPv4 and IPv6), without saying which is which.
- `[UNVERIFIED]` The dashboard raises what the System page shows as Warning or Failed, in one
  banner (red when anything failed, amber otherwise) that names each check and links to the System
  page, and shows no banner when every check passes or is skipped. (Red for a failure, the naming,
  the link, and the banner clearing when every check passed again were seen; a banner for a
  warning alone, which should be amber, wasn't.)

## 9. Traffic history and the charts (an M4 slice, built ahead of the rest of it)

The sampler (`Service.SampleTraffic`, docs/PLAN.md §6.4) adds up each client's bytes in memory and
writes one "raw" row per client a minute, a daily job rolls old rows into hourly ones, and the 1
minute range is read from the last two minutes of 5 s polls, kept in memory and never written to
the database. Three places draw it, all with one range control that every chart shares: the
dashboard, a client's page, and the Charts page (the header icon between Clients and Server
Settings).

- `[VERIFIED 2026-09-30]` The Charts page draws real clients' traffic: Received, Sent, Cumulative
  Received, and Cumulative Sent, stacked at the dashboard chart's width, a line per client that
  moved traffic, a legend, and a tooltip that follows the cursor.
- `[VERIFIED 2026-09-30]` The 1 minute range draws real traffic, from the polls kept in memory.
- `[VERIFIED 2026-09-30]` The dashboard's Bandwidth chart, and a client's own page, show
  Received and Sent as bit rates with a Total in the tooltip, and a client's page adds a Cumulative
  Traffic chart of the same two lines.
- `[VERIFIED 2026-09-30]` On a client's page, Pause, Rename, and Delete are buttons at the top, and
  Delete opens a confirmation that shows its title and message. This was `[FAILED 2026-09-30]`
  first: the confirmation was blank except for its buttons. The page declares `color-scheme: light
  dark`, so a dialog's built-in text color follows the operating system and not the app's own
  mode, and with the system and the app on different schemes the text came out white on the
  dialog's white background. A headless browser reproduces it when the system is dark and the app
  is light, which the tests hadn't tried before.
- `[VERIFIED 2026-09-30]` The y-axis labels aren't clipped on a client's Cumulative Traffic chart
  at 12 hours. This was `[FAILED 2026-09-30]` first: the first digit of "4.0 GB" was cut off. The
  axis was sized to the first of the longest label strings, which is the narrowest of them when
  digits differ in width ("1.0 GB" against "4.0 GB"). A unit test with such digit widths shows
  it, but the headless browser's font has equal-width digits and never did, so only a real
  browser could. The axis is now sized to the widest measured label.
- `[UNVERIFIED]` The x-axis labels on every range, on a real browser's clock: every 15 s (to the
  second) for 1 minute, every 5 minutes for 1 hour, every hour for 12 hours, every 3 hours for 24
  hours, every day for 1 week, every 3 days for 30 days, and every week for 90 days. Times are on
  a 24-hour clock with no am or pm, dates are like `9 Sep`, and no range shows both a date and a
  year. The tick positions and formats are unit-tested (a clock change included), and a headless
  browser has drawn them.
- `[UNVERIFIED]` The 1 week, 30 days, and 90 days ranges, which read the hourly rollup, agree with
  the raw ranges for the same hours. The daily rollup job has to have run past the 48 hour raw
  retention first, so this needs a few days of history.
- `[UNVERIFIED]` After `sudo systemctl restart drawbridge`, the 1 minute range starts empty and
  fills back in within a minute, while every other range keeps its history except the minute that
  hadn't been written yet.
- `[UNVERIFIED]` Renaming a connected client from its page (Rename opens a dialog) changes only
  the name: the peer stays connected, and its config and keys are unchanged.

## 10. AdGuard Home integration (M4; the API client so far)

`internal/adguard` is the REST client the integration is built on (docs/PLAN.md §6.3), and Settings
saves and tests the connection to it. Name sync and the per-client DNS log aren't built yet, so
these steps cover only what is. The client and
its fake were checked on 2026-10-03 against AdGuard Home v0.107.79 in a container on a laptop,
with real queries (CLAUDE.md, "Verified facts"). That isn't the reference platform's install, so
nothing here is `[VERIFIED]` yet.

- `[UNVERIFIED]` The client's contract test passes against the reference platform's AdGuard Home.
  Make an account for it in AdGuard Home first (Settings, then the admin account, or a second
  user in `AdGuardHome.yaml`), and note the version in the result:

    ```bash
    DRAWBRIDGE_ADGUARD_URL=http://127.0.0.1:3000/control DRAWBRIDGE_ADGUARD_USER=drawbridge \
    DRAWBRIDGE_ADGUARD_PASSWORD=... go test -count=1 -v -run Contract ./internal/adguard
    ```

    It adds, renames, and deletes clients named `drawbridge-contract-…` on documentation addresses
    (192.0.2.0/24 and 2001:db8::/32), and leaves nothing behind. It sends no wrong password, because
    AdGuard Home blocks an address for 15 minutes after five.

- `[UNVERIFIED]` Settings has an AdGuard Home section. With the usual address
  (`http://127.0.0.1:3000/control`) and the account, Test connection says "Connected to AdGuard
  Home" with its version, and the daemon, in `drawbridge.service`'s sandbox, reached it (the
  unit allows `AF_INET` and `AF_INET6` and filters no addresses). The VPN addresses list says
  which of them answer DNS, matching the *Check this server* button above it.
- `[UNVERIFIED]` A wrong password gives "refused the account", and clicking Test again right away
  says it won't ask again for 30 seconds. AdGuard Home's own log shows one refused login, not
  two.
- `[UNVERIFIED]` After Save, reloading the page shows the address and the username, an empty
  password field that says "Saved", and no password anywhere in the page, in
  `GET /api/integrations/adguard`, or in `journalctl -u drawbridge`. Changing the address without
  typing the password is refused. The log lists "Changed the AdGuard Home connection", and
  *password: set*, not the password.
- `[UNVERIFIED]` AdGuard Home's query-log warnings: with its log off, and with *Anonymize client
  IPs* on, Test says so.
- `[UNVERIFIED]` Remove forgets the connection: after it, the form shows the usual address and no
  username, and `sqlite3 /var/lib/drawbridge/drawbridge.db 'select count(*) from dns_integration'`
  (as the `drawbridge` user) says 0.
- `[UNVERIFIED]` Name sync on the reference platform's AdGuard Home. Turn on *Use AdGuard Home*
  and Save: every client appears in AdGuard Home (Settings → Client settings) under its name,
  with its IPv4 and IPv6 addresses, and AdGuard Home's query log and statistics show the name
  where they showed `10.8.0.x`. A client of the admin's own is left exactly as it was.
- `[UNVERIFIED]` A synced client is still ad-blocked: from a phone on the VPN, a domain on one of
  AdGuard Home's block lists doesn't load, as before the sync. (In a container, with real DNS
  queries, a synced client's blocked domain was answered `0.0.0.0` like any other address.)
- `[UNVERIFIED]` Renaming a client in Drawbridge renames it in AdGuard Home within a few
  seconds, keeping tags, upstreams, and settings set there. Deleting it deletes its name. Seen from
  Drawbridge's side on 2026-10-05, with name sync already on: a throwaway client's add, rename, and
  delete each logged `integration.adguard_name_added`, `integration.adguard_name_renamed` (from and
  to), and `integration.adguard_name_removed`, from the system, one or two seconds later. What
  AdGuard Home itself showed, and whether tags and upstreams survived the rename, wasn't looked at.
- `[UNVERIFIED]` With a client of the admin's named like a Drawbridge client, Settings and the
  dashboard say one client couldn't be named, and nothing in AdGuard Home changes. Deleting the
  admin's client there, then *Sync now*, names the Drawbridge client.
- `[UNVERIFIED]` With AdGuard Home stopped (`sudo systemctl stop AdGuardHome`), the dashboard says
  Drawbridge can't sync, the journal has one `integration adguard sync failed` warning, and starting
  it again clears both within about 5 minutes (or at once with *Sync now*).
- `[UNVERIFIED]` A client's page has a "Recent DNS Queries" section. With *Use AdGuard Home* on,
  it lists what that client looked up (from a phone on the VPN, visit a few sites), newest first,
  with a blocked domain marked "Blocked" and the filter rule. Nothing from another client appears,
  including one whose address starts with this client's (`10.8.0.2` and `10.8.0.20`). With *Use
  AdGuard Home* off, the section says how to turn it on.
- `[UNVERIFIED]` With AdGuard Home's query log off, or *Anonymize client IPs* on, the section of a
  client with no queries says so, and clears when the setting is changed back and Refresh is
  pressed. (In a container, anonymizing logged every client as `10.8.0.0`.)
- `[VERIFIED 2026-10-03]` AdGuard Home honors the search in "Open this client's queries in
  AdGuard Home" (`#logs?search="10.8.0.2"`, the address in quotes) as an exact match on the
  client's address. Confirmed by the maintainer.
- `[UNVERIFIED]` That link opens AdGuard Home's query log from another device on the home network.
  It goes to the host Drawbridge was opened on, so it works only if AdGuard Home's web interface
  listens on that host's LAN address, not only `127.0.0.1`.

## 11. Read-only API tokens (built ahead of M6)

A token lets a dashboard such as Homepage read the status without logging in (docs/PLAN.md §6.5,
docs/api-tokens.md). In a container, a Node process (as Homepage is) with the daemon's
certificate trusted through `NODE_EXTRA_CA_CERTS` read the status with a token, and was refused
(`ERR_TLS_CERT_ALTNAME_INVALID`) for a name the certificate doesn't have. That isn't real
hardware. Every step below has since passed on the reference platform, with a real Homepage.

- `[VERIFIED 2026-10-03]` The Account page's **API Tokens** section makes a token (after the
  password again), shows it once with a Homepage widget that has the real address, and lists it by
  name and its first 8 characters. After a reload the token isn't on the page.
- `[VERIFIED 2026-10-03]` From a laptop on the home network, `curl -k -H 'Authorization: Bearer
  dbt_…' https://<host>:51821/api/server/status` answers with the tunnel's numbers, and the same
  token gets `403` from `/api/clients/<id>/config` and from `/api/events`.
- `[VERIFIED 2026-10-03]` A real Homepage shows the numbers, set up from docs/api-tokens.md with
  no trouble. If it runs in Docker on the Pi, it needs the Docker network added with `sudo
  drawbridge server set --admin-allow …` and the certificate trusted as that document says; each
  of those two has its own message when it's missing. This Homepage reached Drawbridge by a name
  whose certificate it already trusted (a reverse proxy's, from a public CA), so the certificate
  steps weren't needed, and the document's advice for the certificate problems is still observed
  only in a container. Whether the Docker network step was needed wasn't noted.
- `[VERIFIED 2026-10-03]` The token's "last used" updates, to the hour, while the widget polls.
  Revoking it makes the widget show an error within its next refresh, and nothing else on the Pi
  notices.
- `[VERIFIED 2026-10-03]` `sudo drawbridge admin reset-password` revokes every token (the Account
  page shows none afterward), and the log lists "Revoked every API token (password reset)".
- `[VERIFIED 2026-10-04]` A real Homepage shows the traffic totals (docs/api-tokens.md, "Traffic
  totals"), with the same token that reads the status, and they work well. The run didn't note the
  status numbers falling when a client is paused (a paused client has left the tunnel; the tests
  pin that), so that part is still tested and not observed on hardware.
- `[VERIFIED 2026-10-05]` The gate holds across the whole route table, with a token made for the
  purpose and revoked afterward. On the installed daemon, the 8 routes a token may use (the status,
  the client list and one client, a client's traffic and sessions, and the three traffic totals)
  returned 200, and 18 others returned 403 with "an API token can't use this endpoint": a client's
  config, the event log, the live stream, the settings, the account's own routes (the token list
  included), the System routes (diagnostics, the snapshot list, the backup), the integrations, and
  a client's DNS log. No client row carries a secret field. Per-client routes were tried with a
  throwaway client's id only. On a scratch daemon (`serve --backend fake`, its own database, a
  token made through its API) every non-public route in `internal/api/api.go` was tried, 48 in
  all: 8 answered 200 and 40 answered 403, and none differed. A cookie sent along with the token
  didn't widen it.

## 12. Backup and restore (the second M5 slice)

`drawbridge backup create` makes one encrypted file with the database and the secret key, and
`sudo drawbridge backup restore FILE` puts it back (docs/PLAN.md §6.6, docs/backup-restore.md). The
tests run both against a real SQLite database, a real key, and the real command line. The hardware
pass of 2026-10-05 and 2026-10-06 ran most of it on the reference platform, and each item says what
was seen.

- `[VERIFIED 2026-10-05]` `sudo drawbridge backup create` against the installed daemon asks for the
  passphrase twice with nothing echoed, and writes a file that only root can read. The daemon
  makes it inside its sandbox (a private `/tmp`, and the key read through the `drawbridge`
  group), so check that it doesn't fail on the key or on the temporary files. Note how long it
  takes, and how big the file is, with a few days of traffic history. The maintainer ran it in a
  terminal: the prompt appeared twice and nothing was echoed, and the file was `root:root` 0600 and
  115,943 bytes. Neither the key nor the temporary files failed. A run with `--passphrase-file`
  took 0.45 s and made 115,790 bytes, from a database of 0.8 MB. Claude Code's `!` shell has no
  terminal, so there the command stops with "no passphrase: give one on the terminal, with
  --passphrase-file, or as the first line of standard input" before it creates anything.
- `[VERIFIED 2026-10-05]` The event log has `Made a backup`, from the CLI's account. `sudo
  drawbridge events` lists it as `backup.created` by `root (cli)`, with the file's size and nothing
  secret. (The Logs page's wording wasn't looked at.)
- `[UNVERIFIED]` **The web download:** on the System page, the Backup card with a wrong password
  shows "the current password is wrong" and saves nothing, and with the right one your browser
  saves `drawbridge-<time>.backup`. Do it through the reverse proxy you use for the UI too (a
  proxy that buffers or limits the body could cut it), and once from the VPN. The card then says
  when the last backup was made, and the event log has `Made a backup` from your account (via the
  web) and `Failed to make a backup (wrong password)` for the wrong one, and neither the password
  nor the passphrase. `sudo drawbridge backup restore` of the file you downloaded (with the
  daemon stopped, as in the exit criterion below) works with its passphrase. Note how long the
  click takes on the Pi, since it makes the snapshot and the encryption before the download
  starts. Run so far (2026-10-05): one download with the right password, made through the
  maintainer's reverse proxy from the home network. Its event is in the log from the maintainer's
  web account (115,809 bytes, with no password or passphrase in it). The file arrived whole and
  opens with its passphrase: `backup restore` into scratch paths as a normal user (`--db` and
  `--secret-key` in a scratch directory, `--control` at no socket, `--owner none`) gave a database
  that passes SQLite's integrity check, at schema 11, with every client and the host's own key. Not
  yet run: the wrong password, the VPN, the click's time, and the card's last-backup line.
- `[UNVERIFIED]` The System page's Snapshots card lists the files in
  `/var/lib/drawbridge/backups/` with the right kind, time, and size, newest first, and has no way
  to download one. With `--snapshot-interval 0`, it says the nightly snapshot is off.
- `[UNVERIFIED]` **The exit criterion:** on a freshly flashed card with Drawbridge installed and
  not set up, `sudo systemctl stop drawbridge.service`, `sudo drawbridge backup restore FILE`,
  and `sudo systemctl restart drawbridge-tunnel.service drawbridge.service` bring the old
  server back: log in with the old account, see every client, and a client that was connected
  before reconnects with the config it already has, without being re-added. `ls -l` shows the
  database as `drawbridge:drawbridge` 0600 and `secret.key` as `root:drawbridge` 0640. Not run on a
  fresh card yet. On the host the backup came from (2026-10-06), the same-host item and the
  snapshot item below saw the file modes as listed and a client reconnect with the config it
  already had.
- `[VERIFIED 2026-10-06]` The same on the host the backup came from, after adding a client: the new
  client is gone, the others work, and the `*.before-restore-*` files are there. Run with only the
  daemon stopped (the tunnel unit and `wg0` stayed up, with a throwaway client connected from a
  network namespace over the loopback), after a fresh tar of the state, on the live default paths.
  The backup (131,840 bytes, made in 0.5 s) was made about 2 s before the stop, and a second
  throwaway client was added after it. `sudo drawbridge backup restore FILE` took 0.5 s and printed
  "Restored the backup made ...", "Every login in it was ended (1)", and the two files it kept,
  `drawbridge.db.before-restore-<time>` and `secret.key.before-restore-<time>`. On copies of the
  restored files, SQLite's integrity check passed, the schema was 11, the clients were the five real
  ones and the first throwaway without the one added after the backup, and every table matched a
  scratch restore of the same backup except the event log, which has the restore's own event. The
  real clients' rows (keys, addresses, config record), the server's settings and key, and the admin
  account were identical to before. The key's sha256 was the one recorded before the pass,
  `secret.key` was `root:drawbridge` 0640, and the database `drawbridge:drawbridge` 0600. The daemon
  was started alone with `systemctl start drawbridge.service` (the restore's own printed next step
  restarts both units, which would have recreated `wg0`). The web UI answered 0.12 s later, the
  daemon had been away for 3.4 s in all, and the kernel's peers followed the restored database
  within 0.14 s: the peer of the client added after the backup was removed, and logged as
  `tunnel.drift_corrected`, a warning in the journal. `wg0`, the tunnel unit, and the firewall
  revision didn't change, `drawbridge doctor` passed 13 of 13, and the journal said the installed
  TLS certificate was in use. The connected throwaway client lost nothing: across the stops it had
  675 replies with a longest gap of 0.21 s, and its session carried on with no new event. With name
  sync on, the AdGuard Home entry made for the client added after the backup stayed after the
  restore: the restored database has no record of it, so nothing removes it. Adding a client of the
  same name again, which got the same addresses, adopted the entry with no write, and deleting that
  client removed it a second later. The guide's "Not in a backup" says the names "come back with the
  next sync", which is the other direction, and doesn't mention this leftover.
- `[VERIFIED 2026-10-05]` A wrong passphrase, a file with one byte changed, and a file cut short are
  each refused, and nothing is written. Run against scratch paths, as a non-root user (`--db` and
  `--secret-key` in a scratch directory, `--control` at no socket, `--owner none`), on a backup made
  for the purpose. A wrong passphrase, a byte flipped in the first chunk (at offset 100, and in the
  middle), and a file cut in half were each refused with "wrong passphrase, or the backup is
  damaged. Nothing was changed". A byte flipped in the last chunk, and a file one byte short, were
  refused with "the backup is damaged or cut short. Nothing was changed". The exit status was 1 each
  time and the scratch directory stayed empty. (The first chunk is what proves the passphrase, so
  damage there reads as a wrong passphrase.)
- `[VERIFIED 2026-10-06]` The same against the live files, with the daemon stopped: the host's
  database and key are as they were. Run as root on the live default paths, after a fresh tar, with
  the same five bad inputs on a 131,591-byte backup made for the purpose. Each was refused with exit
  status 1 and "Nothing was changed": a wrong passphrase ("wrong passphrase, or the backup is
  damaged"), a flipped byte in the middle, a file cut in half, a flipped last byte, and a file one
  byte short (the last four "the backup is damaged or cut short"). After each, a fingerprint of
  `/etc/drawbridge` and `/var/lib/drawbridge` (every name, owner, mode, and size, and the sha256 of
  every file) was identical to the one taken before the first, so nothing was written and no
  temporary file was left behind. Which message a damaged file gets depends on the chunk the damage
  is in: the middle of a 130,812-byte file read as a wrong passphrase in a scratch run, and the
  middle of this one, 800 bytes longer, as "damaged or cut short".
- `[VERIFIED 2026-10-05]` With the daemon running, `restore` refuses and says to stop it. `sudo
  drawbridge backup restore FILE`, on the live default paths with nothing on standard input,
  printed "the Drawbridge daemon is running. Stop it first: sudo systemctl stop drawbridge.service"
  and exited 1. The key and the database were untouched.
- `[UNVERIFIED]` A backup made by the previous release restores onto this one, and the database is
  migrated (`TestRestoreFromEverySchema` checks this for every earlier schema, with data written
  by hand; §18 has the part that needs a real older build).
- `[VERIFIED 2026-10-06]` After a restore, the browser tab that was logged in to the old host is
  logged out. The maintainer's tab, logged in through their reverse proxy, went to the login page
  by itself after a restore (their report; the second restore of the day ended the login they had
  made after the first). Each restore printed "Every login in it was ended", and no row was left
  in the login table.
- `[VERIFIED 2026-10-06]` An API token made before the backup still works after a restore. The
  maintainer's dashboard (Homepage, which reaches the API by address and port, not through the
  reverse proxy) uses the one API token, made two days before the backups. After the restores its
  Drawbridge widget showed numbers that matched Drawbridge's own dashboard (their report). The
  database also had the token's row, identical, before and after every restore.
- `[VERIFIED 2026-10-05]` Nightly snapshots: a minute after the daemon starts on a host with none,
  one appears in `/var/lib/drawbridge/backups/` (0600, in a 0700 directory, owned by `drawbridge`),
  and a restart doesn't make another. With `--snapshot-interval 1m` (a scratch run), a new one comes
  every few minutes and only the newest `--snapshot-keep` stay. (Seen in a container with the fake
  backend: the daemon made one 60 seconds after it started, mode 0600 in a 0700 directory, a restart
  made no second one, and `backup restore` took it. Now seen on the Pi too.) On the Pi, a scratch
  daemon (`serve --backend fake` as a normal user, a fresh database, `--snapshot-interval 1m
  --snapshot-keep 3`) made its first snapshot exactly 60 s after it started and one more each
  minute after that, deleted the oldest from the fourth on, and never held more than 3 in 7 minutes
  (the files were 0600 in a 0700 directory). The installed daemon's own snapshots, a day apart, are
  0600 in a 0700 directory owned by `drawbridge`, and two restarts of it (a stop and a start, and an
  upgrade's `systemctl restart`) made no new one.
- `[UNVERIFIED]` An upgrade that adds a migration (the next release that does) leaves a
  `pre-migration-v<n>-*.db` before the schema changes, made by `drawbridge-tunnel.service` or the
  daemon, whichever opened the database first, and the journal says "upgraded the database". With
  the directory made unwritable, the upgrade is refused with a message that says the snapshot
  failed, and the old version of the database is untouched.
- `[VERIFIED 2026-10-06]` `sudo drawbridge backup restore` of a nightly snapshot, with the daemon
  stopped, asks for no passphrase, keeps the host's key, and brings the clients back as they were
  when it was made; a client added since is gone. Run on the newest nightly snapshot (7 hours old),
  on the live default paths, after a fresh tar, with nothing on standard input. It printed "Restored
  the snapshot made ...", said the key was unchanged ("a snapshot opens with this host's own key"),
  and kept only a copy of the database: no copy of the key was made, and `secret.key` was
  byte-identical to the one in the tar. Before the restore, the snapshot's real-client rows, server
  settings, and admin account were checked equal to the live ones, so nothing real could be lost.
  Afterward every table matched the snapshot except the event log, which has the restore's own
  event (`source: snapshot`), and the two throwaway clients added since were gone. With the daemon
  started on it (the web UI answered 0.12 s later), the kernel's peers were the five real ones
  within 0.12 s, the real peers' endpoints, handshakes, and byte counters were exactly as before,
  and `wg0` and the firewall revision hadn't changed. A throwaway client connected from a namespace
  got no replies for 8.6 s, until a second restore, of a fresh backup, put its client back: it
  shook hands again 0.41 s after the daemon started, with the config it already had and without
  being re-added, and the daemon closed its old session and opened a new one in the same second
  (a re-added peer's byte counters start again). That second restore put back everything the
  snapshot had replaced: every table matched the backup except the event log, and the real
  clients' rows, the settings, and the account were identical to what they had been at the start.
  One wart: a real client's session was open in the snapshot, and the daemon on the snapshot's
  data ended it at once with a `client.disconnected` event dated that moment, with a duration of
  9 hours counted from the start of the session in the snapshot, and its byte counts. The daemon
  can't know when it really ended. The backup restore after it threw that event away with the
  rest of what the snapshot had replaced.

## 13. Outdated configs and client key rotation (the third M5 slice)

A client is flagged "config outdated" when the server's settings or its own keys change after the
admin last handed out its config, and `drawbridge client rotate-keys` gives a client new keys
(docs/PLAN.md §6.1). Nothing here has run on the reference platform, so nothing is `[VERIFIED]`
yet. What has run, away from it: the kernel test (`TestEndToEnd`'s rotation step) passes in
network namespaces on a 7.0 aarch64 kernel (Docker Desktop's VM): after a rotation the server has
only the new peer, the old config can't fetch anything through the tunnel, and the new one
connects over IPv4 and IPv6. And the previous release's database (schema 8) was opened by this
build with the fake backend: it migrated to schema 9 and took `pre-migration-v8-*.db` first, the
clients were kept and none was flagged, and a client's baseline was set the first time its config
was viewed.

- `[UNVERIFIED]` **The upgrade:** `sudo apt install ./drawbridge_*.deb` over the previous release
  (schema 8) restarts both units, the journal says "upgraded the database" with `from_schema=8
  to_schema=9`, `/var/lib/drawbridge/backups/` has `pre-migration-v8-*.db`, the tunnel stays up
  and a connected client stays connected. This is the first release that adds a migration, so it
  is also the first real run of the snapshot-before-migrating step (§12's last items). Every
  client reads "no record" for its config at first, and none is flagged outdated. The maintainer's
  own upgrade on 2026-10-04 (not watched) is in the journal: `upgraded the database` with
  `from_schema=8 to_schema=9` and the snapshot `pre-migration-v8-20261004-172941.db`, which is
  still in `backups/`. The tunnel unit did not restart, and nobody was connected.
- `[VERIFIED 2026-10-05]` **Handing out a config sets the baseline, from the command line:**
  `drawbridge client show NAME` says "no record of it being handed out" for a new client, and
  "current, last handed out 0s ago" right after `client config NAME` and, on another new client,
  right after `client qr NAME`. `client list` shows `current` in its CONFIG column.
- `[VERIFIED 2026-10-06]` **The same from the web UI:** after "Download .conf", and after "Show QR
  code". The maintainer tried both in the browser and reported that each worked as expected (the
  browser side is theirs; it wasn't watched here). The server's log has three `client.config_viewed`
  events from their web session, 17 s apart in all, on two real clients, and the one that had never
  been handed a config reads `current` in `client list` since (it read `-` before). They then
  imported the downloaded `.conf` on a laptop and scanned the QR code on a phone, and both devices
  connected within a minute of the last view (`client.connected`) and ran traffic through the
  tunnel for three minutes each: by the server's counters the phone received 264 MiB and sent
  202 MiB, and the laptop 145 MiB and 166 MiB.
- `[VERIFIED 2026-10-05]` **A server change flags it, for the DNS servers:** with a throwaway client
  that had been handed its config, `drawbridge server set --dns 1.1.1.1,1.0.0.1` made `client show`
  say "outdated (last handed out 0s ago; the server's settings or the client's keys changed
  since). Hand out the new one with `client qr` or `client config`" and `client list` say
  `outdated`, at once; `server set --dns server` put it back to `current` with no new download. The
  event log has both changes, as `dns: [..] → [..]`.
- `[UNVERIFIED]` **The rest:** change the MTU (`drawbridge server set --mtu`) and the client's row
  and page get a "Config outdated" badge within a few seconds, the dashboard's Outdated tile counts
  it and opens the list filtered to it. Putting the MTU back leaves the client current again, with
  no new download. Changing the endpoint and the keepalive does the same; turning client isolation
  on or off does not (the firewall isn't in the config). Only the DNS change was tried on the real
  server. The badge and the tile were seen in the browser only after a key rotation, not after a
  change like these (§15).
- `[UNVERIFIED]` **With a real phone:** import a config in the WireGuard app, change the MTU, and
  the badge appears; scan the new QR code, the badge goes, and the app shows the new MTU.
- `[UNVERIFIED]` **Rotating keys:** on a connected phone, "Rotate keys" on its page (the popup asks
  first; Cancel changes nothing) cuts it off within a handshake interval, shows the new QR code at
  once, and the old config never connects again. Scanning the new code reconnects it. The event
  log has `Rotated a client's keys` from the admin and the new public key, and no secret. The
  client's session closes with a `Disconnected` event, then a new one opens. Seen on the Pi, on the
  server's side, with a throwaway client connected from a network namespace: its WireGuard
  interface is created in the host's namespace (so its UDP socket stays there), then moved into the
  client's, and its endpoint is `127.0.0.1:51820`, so nothing is added to the host's network.
  `client rotate-keys NAME --yes` cut it off at once (it pinged the server's VPN addresses fine just
  before; the old config got no answer in four tries over 16 s, and the server's new peer never
  had a handshake from it). `client.keys_rotated` and a `client.disconnected` with the session's
  duration and bytes were logged in the same second. The event holds the new public key, and
  neither the old nor the new private key is in the journal or the event log. The new config (from
  `client config`) pinged over IPv4 and IPv6 and opened a new session 2 s later. Not run: the
  popup, the QR code on the page, and a phone.
- `[VERIFIED 2026-10-05]` `sudo drawbridge client rotate-keys NAME` asks `[y/N]` on a terminal,
  refuses without `--yes` when its input isn't one, and prints the commands for the new QR code and
  config. On a pseudo-terminal, `n` printed "Keys not rotated." and exited 1 with the key
  unchanged, and `y` rotated the keys and printed "Show its QR code:   drawbridge client qr NAME"
  and "Save its config:    drawbridge client config NAME > NAME.conf". With `y` on a pipe it
  printed "not a terminal; add --yes to go ahead without asking" and exited 1, with the key
  unchanged. With standard input at `/dev/null` it printed the prompt, read nothing, and answered
  no (exit 1, keys unchanged), so only a pipe gets the "not a terminal" message.
- `[VERIFIED 2026-10-05]` A read-only API token gets 403 from `POST /api/clients/{id}/rotate-keys`,
  and sees `config_outdated` and the `outdated` count in the client list and the status (they hold
  no secret). With a throwaway client's id the POST returned 403, and no keys were rotated. After
  a DNS change (the flag item above) the client list had `config_outdated: true` on the throwaway
  and `/api/server/status` said `outdated: 2` (the throwaway and the one real client that had been
  handed a config); after the DNS setting was put back, the field was gone and the count was 0. No
  client row carries a private key or a preshared key.

## 14. Safe apply and `drawbridge apply` (the fourth M5 slice)

A settings change that could cut the admin off (the listen port, removing a source from the admin
UI's allowlist) is applied on probation: undone after 60 seconds unless it's kept (docs/PLAN.md
§4.3). The hardware pass of 2026-10-05 and 2026-10-06 ran most of it on the reference platform, and
each item says what was seen. Before that, what had run, away from it: `TestEndToEnd`'s two new
steps pass in network namespaces on a 7.0 aarch64 kernel (Docker Desktop's VM). With real kernel
WireGuard, a `server set --port 51999 --safe` cut the client off (its fetches through the tunnel
failed), the daemon put the port back when the window (4 seconds there) ran out, and the client was
back without being touched. `apply --dry-run` listed a drifted MTU and left it alone, and `apply`
fixed it. And the browser tests drive the Keep, Undo now, and not-kept paths against the daemon with
the fake backend and a 10-second window.

- `[UNVERIFIED]` **From a phone on the VPN, the case it is for:** connect the phone to the VPN,
  open the web UI through it, and change the listen port in Settings. The UI answers once and then
  stops (the phone's tunnel is dead: it still sends to the old port), and the router forwards only
  the old port. Within a minute the daemon puts the port back, the tunnel comes back by itself
  (WireGuard retries within about 15 seconds), and the Settings page shows the old port. The event
  log has `Changed server settings` (with `waiting to be kept: 1m0s`) and then `Undid a settings
  change (not kept in time)`, from Drawbridge. The next item ran the same change with a throwaway
  client in place of a phone; a real phone, and the router, still haven't.
- `[VERIFIED 2026-10-06]` **The listen port itself, with a throwaway client standing in for the
  phone.** The real server's port moved from 51820 to 51999 (nothing else listens there) with
  `sudo drawbridge server set --port 51999 --safe`, four times, with a fresh tar of the state
  before the first and the second, and no real client on the VPN (the newest handshake was over
  four hours old). The throwaway was in a network namespace on the host with its endpoint on the
  loopback, and fetched the web UI through the VPN once a second. Each time the command returned
  in 0.04 to 0.06 s, `wg show` had the new port, only the new port was listening (looked at by
  hand in the first window), the firewall's revision was the same (its ruleset has no WireGuard
  port in it), and `server show` listed the change with its countdown. The client's pings and
  fetches failed within seconds. **Not kept:** the daemon put the port back 61.07 s after the
  change, and logged
  `server.settings_expired` from `drawbridge (system)` ("not kept within 1m0s"); the change's own
  event, from `root (cli)`, carries `waiting_to_be_kept: 1m0s`. **Kept:** `server confirm`, 13 s in,
  printed "Kept the change." and logged `server.settings_kept`; 72 s after the change the new port
  was still in place with nothing waiting, and then `server set --port 51820` put it back at once.
  **Undone:** `server revert`, 11 s in, printed "Undid the change. The settings are back as they
  were." and logged `server.settings_undone`; the port was back in 0.04 s, and the client's tunnel
  carried on 0.08 s later. **A daemon restart inside the window:** see the item on restarting the
  daemon below. What the client did: after the port moved, its tunnel was dead for about 15 s and
  then came back by itself on the new port, with the change still waiting. A packet from the
  server's new port had reached it: `wg show` on both ends had a handshake about 15 s into the
  window, and the client's endpoint for the server read `127.0.0.1:51999`. (The likely sender is
  the server's retry after 15 s without a reply.) After the port went back, it was dead for 10 to
  15.5 s and
  came back by itself again (the first ping reply came 15.4, 10.3, and 15.0 s after the port was
  back in the three windows that lasted long enough; a revert inside 15 s resumed the old session
  at once). On the host, the real peers' endpoints, handshakes, and byte counters were the same
  before and after, the tunnel unit and `wg0` were never touched, and the journal had no warning.
  This run had no router or NAT. Through a NAT, a packet from a new port may not reach a phone, so
  the phone item above stays `[UNVERIFIED]`.
- `[VERIFIED 2026-10-05]` **From the LAN:** the same change shows a bar on every page with a
  countdown. **Keep changes** leaves the new port in place past the minute (check `sudo wg show`),
  and the log has `Kept a settings change` from the admin. **Undo now** puts the old port back at
  once. Run with a harmless change in place of the port, which would have cut real clients off: an
  extra admin source (`100.64.10.0/24`) added, then removed with `server set --admin-allow none
  --safe`. The bar showed on every page the maintainer visited, with a countdown. **Keep changes**
  (pressed 26 s into one window and 8 s into another) kept the change, and the log has
  `server.settings_kept` from the maintainer's web account. **Undo now** (20 s into a third) put the
  extra source back at once, in the setting and in the firewall's `admin_allowed4` set, and the log
  has `server.settings_undone` from the web account. A window nobody touched was undone by the
  daemon at 61 s (`server.settings_expired`). The port itself wasn't changed in that run; it was on
  2026-10-06, from the command line (the previous item).
- `[VERIFIED 2026-10-05]` The bar is readable in dark mode (on the maintainer's laptop: "looks
  fine"), as it was in light mode.
- `[UNVERIFIED]` The bar is readable on a phone, and its countdown is right when the browser's clock
  is wrong by a few minutes (set one wrong to try).
- `[VERIFIED 2026-10-06]` **A reboot inside the window:** change the port from the LAN, don't keep
  it, and `sudo reboot` straight away. After the boot the tunnel has the new port for a moment
  (`drawbridge-tunnel.service` applies what the database says), and then the daemon undoes it: the
  port is back to the old one, and `server show` has nothing waiting. Run on the real port with
  `server set --port ... --safe` and `systemctl reboot` 45 s into the window (the Pi reaches
  userspace in about 12 s, so an earlier reboot could be back before the deadline). The tunnel
  unit's `tunnel up` logged `listen_port=51999` at boot, 0.2 s before the daemon started. The
  daemon logged `server.settings_expired` (from `drawbridge (system)`, "not kept within 1m0s") 9 s
  after it started, and `applied the undone settings` (`set the listen port to 51820`) 40 ms later,
  so the port was wrong for about 9 s. Afterward the kernel's port was 51820, nothing was waiting,
  and `server show`, the firewall's revision, `wg show` (key, port, peers, allowed IPs, keepalive),
  `client list`, and the secret key were the same as just before the change; doctor was at 13 of
  13 and both units were new invocations. **The clock matters:** the wall clock at boot started
  near the time of the shutdown and was stepped forward about two minutes later, so the daemon saw
  the deadline 9 s ahead and let the countdown run out, rather than finding it already past. The
  events from those minutes carry the early clock (the expiry reads 19:58:05 though it happened
  about two minutes later by the real time). Also seen: at boot the tunnel unit applied firewall
  revision `4bf62ca2c278` and the daemon corrected it to the steady-state one a couple of minutes
  later (`tunnel.drift_corrected`); the same revision is in the journal of the 2026-10-03 boot.
  No real client was connected, so a phone's reaction wasn't seen.
- `[VERIFIED 2026-10-06]` **Restarting the daemon:** `sudo systemctl restart drawbridge.service`
  inside the window leaves the change waiting, with the countdown carrying on from where it was.
  Run on the real listen port, 12 s into a window, when the countdown read 45 s. The CLI answered
  again 0.12 s after the restart began, `server show` still listed the change, the countdown was
  still counting down (not back at 60 s), and the kernel still had the new port. The daemon undid
  the change 60.30 s after it was made, at the original deadline and not a minute after the
  restart, and logged `server.settings_expired`. (By the code, the deadline is a time saved with the
  change, so a restart that outlasts it should undo the change as soon as the daemon starts; the
  reboot item below is where that gets run.)
- `[UNVERIFIED]` Removing the source the browser is on from the admin UI's allowlist
  (`sudo drawbridge server set --admin-allow none --safe`, then reload from that source) locks the
  browser out; a minute later the source works again, with nothing done.
- `[VERIFIED 2026-10-05]` While a change waits, another settings change from `server set` is
  refused and says to keep or undo the first, and adding, pausing, and deleting clients still work.
  A removal of an extra admin source made with `--safe` waited, and a real DNS change made then was
  refused with "a settings change is waiting to be kept or undone; keep it or undo it first. Keep
  it with `drawbridge server confirm`, or undo it with `drawbridge server revert`." (exit 1, and
  the DNS setting unchanged). A throwaway client was added, paused, resumed, and deleted inside
  the same window. (`client delete` asks `[y/N]`, and with no terminal it needs `--yes`.)
- `[UNVERIFIED]` The same refusal from the web UI's Settings. (Tried once on 2026-10-05, during a
  window, and the message wasn't noticed. A save's result shows in a box directly above the **Save
  settings** button at the bottom of the form, not at the top of the page.)
- `[VERIFIED 2026-10-05]` `sudo drawbridge server show` lists a waiting change, with who made it and
  the seconds left; `sudo drawbridge server confirm` and `revert` work from a terminal. The change
  was a harmless one: adding `--admin-allow 100.64.10.0/24` (applied at once), then removing it
  with `--safe`. `server show` printed "Waiting to be kept (made by root via cli): admin allowed:
  [100.64.10.0/24] → none. It is undone in 60 s unless you keep it." and ten seconds later "undone
  in 51 s", and the firewall's `admin_allowed4` set lost the prefix at once. `server confirm`
  ("Kept the change.") kept it and logged `server.settings_kept` from `root (cli)`. A second time,
  `server revert` ("Undid the change. The settings are back as they were.") put the prefix back at
  once and logged `server.settings_undone`. A third time, with neither, the daemon undid it 60 s
  after it was made (still waiting at 58 s, undone by 61 s) and logged `server.settings_expired`
  from `drawbridge (system)`, "not kept within 1m0s". The change's own event carries
  `waiting_to_be_kept: 1m0s`. Afterward `server show`, the nft table, and the peers were the same
  as before the test.
- `[VERIFIED 2026-10-05]` The web UI's bar does the same for a change made with `--safe` on the
  command line: **Keep changes** and **Undo now** from a browser (the run is described under
  "From the LAN" below).
- `[VERIFIED 2026-10-05]` `sudo drawbridge apply --dry-run` after `sudo ip link set wg0 mtu 1500`
  (and before the daemon's 30-second check notices) lists the MTU and changes nothing; `sudo
  drawbridge apply` puts it back. With the tunnel stopped, both say so and start nothing. After the
  drift, `apply --dry-run` printed "Would change: set the MTU to 1420" and `wg0` was still at 1500
  right after it; `apply` printed "Changed: set the MTU to 1420", logged `tunnel.applied` from `root
  (cli)`, and `wg0` read 1420; a second dry run said "Nothing to change: the tunnel and the
  firewall match the settings." With `drawbridge-tunnel.service` stopped (`wg0` and the nft table
  gone, the daemon still active), both commands printed "The tunnel is stopped, so there is nothing
  to apply. Start it with: sudo systemctl start drawbridge-tunnel" and exited 0, and `wg0` was
  still absent afterward. `systemctl start` then brought back `wg0` as a new interface (a new
  index) with the same peers and keys, and the same nft revision, 2.6 s after the stop began.
  (`doctor` with the tunnel stopped failed the Tunnel check, "so no client can connect".) What
  doesn't come back is the kernel's per-peer runtime state, which belongs to the interface: after
  the stop and start every client read "never" for its handshake, no endpoint, and 0 B received and
  sent (the totals in `client list`; one client had had 5.5 GiB). Configs, keys, settings, and the
  database's traffic history are untouched, and a phone's next handshake brings its endpoint back.
- `[UNVERIFIED]` An upgrade from the previous release (schema 9) takes `pre-migration-v9-*.db`
  and adds the `pending_apply` table; nothing is waiting afterward. The maintainer's own upgrade on
  2026-10-04 (not watched) is in the journal (`from_schema=9 to_schema=10`, the snapshot
  `pre-migration-v9-20261004-183703.db`, still in `backups/`); the database has the `pending_apply`
  table with no rows, and `server show` lists nothing waiting.

## 15. Rotating the server's key (the fifth M5 slice)

`drawbridge server rotate-key`, the **Rotate the key…** button in Settings, and
`POST /api/server/rotate-key` give the server a new key pair (docs/PLAN.md §6.2). Every client's
config holds the old public key, so every client stops until it imports its new config. The web UI
always puts the rotation on safe apply (§14 above). The hardware pass of 2026-10-05 and
2026-10-06 ran most of it on the reference platform, and each item says what was seen. Before
that, what had run, away from it: `TestEndToEnd`'s two new steps pass in network namespaces on a
7.0 aarch64 kernel (Docker Desktop's VM). With real kernel WireGuard, after `server rotate-key` the
kernel had the new private key and the same peer, a connected client's fetches through the tunnel
failed at once (WireGuard drops the current sessions when the interface's key changes), and the
new config connected over IPv4 and IPv6. After `rotate-key --safe` with nothing kept, the daemon
put the old key back when the window (4 seconds there) ran out, and the client reconnected with
the config it already had. And the browser tests drive the dialog, Undo now, and Keep against the
daemon with the fake backend.

- `[VERIFIED 2026-10-06]` **From the LAN, the web dialog:** in Settings, **Rotate the key…** asks
  first (Cancel changes nothing), and **Rotate the key** shows the bar with both public keys. Run
  by the maintainer in their browser, through their reverse proxy, with a throwaway client in a
  network namespace standing in for the phone. The dialog warned that every client stops working
  at once, and **Cancel** changed nothing (their report; the server's key was the same afterward,
  nothing was logged, and nothing waited). **Rotate the key** changed the kernel's public key and
  logged `server.key_rotated` from their web account, with `waiting_to_be_kept: 1m0s` and the old
  and the new public key. `server show` listed "Waiting to be kept (made by <the account> via web):
  server public key: <old> → <new>. It is undone in 59 s unless you keep it." They reported that
  the bar showed both public keys and a countdown, and that the note appeared on Settings. The
  throwaway's tunnel stopped within seconds (its pings got no replies, and a fetch of the web UI
  through the VPN failed). Nothing was pressed, the daemon put the old key back 60.63 s after the
  change (`server.settings_expired` from `drawbridge (system)`), and after a reload Settings
  showed the original key again (their report).
- `[VERIFIED 2026-10-06]` **The case it is for, with a throwaway client standing in for the
  phone:** rotate the key, and the web UI fetched through the VPN stops answering; within a minute
  the daemon puts the old key back, and the client's old config works again without being touched.
  Run three times on the real server (the web dialog above, and `server rotate-key --safe --yes`
  twice), with a client in a network namespace that sent a ping every 0.2 s and fetched the web UI
  through the VPN once a second. Each time the pings and the fetches failed until the old key was
  back: 60.63 s after the change when nobody touched it, 60.32 s in a window with a daemon restart
  in it, and 0.02 s after `server revert`. The client got through again 0.01, 0.01, and 0.07 s
  after the old key was back (it was sending all the time), and the web UI through the VPN
  answered again within 0.09 to 0.62 s. The log has `server.key_rotated`, and then
  `server.settings_expired` (from `drawbridge (system)`) or `server.settings_undone`. This had no
  phone and no router, so the next item stays `[UNVERIFIED]`.
- `[UNVERIFIED]` **A real phone, and Keep from the bar:** with the web UI open through the VPN on a
  phone, rotate the key. The UI stops answering (the phone's tunnel is dead). Within a minute the
  daemon puts the old key back, the tunnel comes back by itself, and the phone's old config works
  again without being touched. Keep a rotation from the bar, and `sudo wg show` has the new public
  key; the phone stays off until it imports its new config (the client's page shows **Config
  outdated**, and its QR code is the new one). Seen so far, with the throwaway and no phone: from
  the command line (`server confirm`), the new key was in the kernel past the minute, the
  throwaway's old config stayed off, and its new config connected (the items below). From the bar,
  on 2026-10-06 the maintainer pressed **Keep changes** 30 s after a rotation (the log has
  `server.settings_kept` from their web session): the new key was in the kernel past the minute and
  stayed until the original was restored 3 min 52 s after the rotation, and the maintainer saw the
  page of the real client that had been handed a config say **Config outdated**. Not seen: a phone,
  and the new QR code.
- `[VERIFIED 2026-10-06]` After a kept rotation, `sudo drawbridge client list` shows the clients
  that had been handed a config as outdated, and a client that was never handed a config isn't
  flagged. Importing each new config clears its flag. Run on the real server, kept with `server
  confirm`: of six clients, the two that had been handed a config (one real, one throwaway) read
  `outdated`, and the four that never had still read `-`. The rotation's own message said "2 of the
  clients hold a config with the old key." Handing out the throwaway's new config (`client config`)
  made it `current` again and left the other `outdated`, and that config connected over IPv4 and
  IPv6 (the first reply 3.7 s after its client was recreated), while its old config had stayed off.
- `[VERIFIED 2026-10-06]` The dashboard's Outdated count is that same number. In a second kept
  rotation on the real server, the maintainer read it in the browser: 2 while the rotation was
  kept, 1 after the throwaway's new config was handed out, and 0 after the original key was back
  (the restore ended their login, and they logged in again). The new key was held 3 min 52 s. The
  backup made seconds before was restored with only the daemon stopped (away 0.73 s in all), the
  original key was back in the kernel 0.14 s after the daemon started, the real clients' rows were
  identical to before, `drawbridge doctor` was at 13 of 13, and the throwaway's original config
  connected again (3.65 s after its client was recreated).
- `[VERIFIED 2026-10-05]` `sudo drawbridge server rotate-key` asks `[y/N]` on a terminal and
  refuses without one unless given `--yes`. It was only ever answered no, and always with
  `--safe`, so that a mistake could not have lasted. On a pseudo-terminal, `n` printed "The key was
  not rotated." and exited 1. With `n` on a pipe it printed "drawbridge: not a terminal; add --yes
  to go ahead without asking" and exited 1. With standard input at `/dev/null` it printed the
  prompt, read nothing, and printed "The key was not rotated." (exit 1). The server's key, the nft
  revision, and the lack of a waiting change were the same afterward, and no rotation was logged.
- `[VERIFIED 2026-10-06]` Without `--safe` it applies at once and waits for nothing, and with it
  `server confirm` and `server revert` work. While a rotation waits, another settings change is
  refused. Run on the real server, which cut the real clients off for a minute and a half while the
  rotation was kept. `server rotate-key --yes` printed "The server has a new key. Public key: ...
  2 of the clients hold a config with the old key. Hand each one its new config ..."; nothing was
  waiting afterward, the kernel had the new key, and the event had no `waiting_to_be_kept`. With
  `--safe`, `server confirm` printed "Kept the change." and logged `server.settings_kept` from `root
  (cli)`, and the new key was still in the kernel at 72 s; `server revert` printed "Undid the
  change. The settings are back as they were.", put the old key back in 0.02 s, and logged
  `server.settings_undone`. While a rotation waited, `server set --keepalive 25` (the value it
  already had, so a mistake would have changed nothing) exited 1 with "a settings change is waiting
  to be kept or undone; keep it or undo it first. Keep it with `drawbridge server confirm`, or undo
  it with `drawbridge server revert`.", and the rotation was still waiting afterward.
- `[VERIFIED 2026-10-05]` A read-only API token gets 403 from `POST /api/server/rotate-key`. The
  handler needs no body and no password, so a failed gate would have rotated the real server's key
  (on probation). The request was sent only while a harmless change was waiting to be kept (an
  extra admin source removed with `--safe`), which refuses any second change, so that even a
  failed gate couldn't rotate anything. It returned 403, and so did `POST /api/server/apply/confirm`
  and `/revert`: the change stayed waiting, and the server's key and the nft revision were the same
  afterward.
- `[VERIFIED 2026-10-06]` A restart inside the window leaves a rotation on probation, and the old
  key is back at its deadline, like any other change on probation. `sudo systemctl restart
  drawbridge.service` 10 s into a rotation window on the real server: the CLI answered again 0.11 s
  after the restart began, the rotation was still waiting with the countdown carried on (49 s left
  before and after), and the kernel still had the new key. The daemon put the old key back 60.32 s
  after the change, at the original deadline and not a minute after the restart, `sudo wg show`
  agreed, and `server.settings_expired` was logged from `drawbridge (system)`.
- `[UNVERIFIED]` A reboot inside the window undoes a rotation like any other change on probation:
  the old key is back afterward, and `sudo wg show` agrees. (§14 has the same item for the port.)
- `[VERIFIED 2026-10-06]` A backup made before a rotation holds the old key, and restoring it
  brings the old key back, and the clients' configs from before the rotation work again. Run on
  the host the backup came from, not a fresh one. A backup was made about 1 s before the first of
  two rotations that were both kept (one with `--safe` and `server confirm`, one without `--safe`),
  after a fresh tar of the state. With only the daemon stopped, `sudo drawbridge backup restore`
  of it put the server's row (its settings and the sealed key), every real client's row, and the
  throwaway's row back exactly as they were before the first rotation. The daemon, started alone
  with `systemctl start`, set the kernel's private key back 0.14 s after it started (the event
  `tunnel.drift_corrected: set the private key`, a warning in the journal), so the tunnel unit
  didn't need a restart. The throwaway's config from before the rotation connected again over IPv4
  and IPv6 (the first reply 3.7 s after its client was recreated), `client list` was as at the
  start, `drawbridge doctor` passed 13 of 13, and the real peers' endpoints, handshakes, and byte
  counters were exactly as they had been before the rotations. The restore ended two logins.
- `[UNVERIFIED]` The same onto a fresh host (§12's exit criterion is that restore).
- `[VERIFIED 2026-10-06]` A rotation puts no private key where an admin can read it. After each of
  five rotations on the real server (one from the web, three with `--safe`, one without), the
  server's old private key, the key it was rotated to, and the current one appeared nowhere in the
  event log, `server show`, the journal of both units, or the CLI's own output: no match for any
  of them (the keys were read into a shell variable and compared there, and never printed). The
  event carries only the old and the new public key.

## 16. Your own TLS certificate (the sixth M5 slice)

`drawbridge tls install|show|reset`, the **Web UI Certificate** card on the System page, and
`GET|PUT|DELETE /api/system/certificate` serve a certificate the admin brings instead of the
self-signed one (docs/PLAN.md §6.6, docs/tls-certificate.md). Nothing here has run on the
reference platform, so nothing is `[VERIFIED]` yet. What has run, away from it: the browser tests
install a certificate made by openssl through the page and through the CLI against the real daemon
(the fake backend), then open a raw TLS connection to the daemon and read the certificate it
presents, which was the new one at once and the self-signed one again after a reset. The Go tests
do the same through a TLS listener built from the store's configuration. No real certificate
authority, no real browser trust store, and no ACME client has been involved.

- `[UNVERIFIED]` With a certificate from a CA your browser trusts (Let's Encrypt by the DNS-01
  challenge, say), for a name that resolves to the host on your network: `sudo drawbridge tls
  install --cert fullchain.pem --key privkey.pem` succeeds without a restart, and a fresh load of
  `https://<that name>:51821` shows the padlock with no warning. The page you installed from keeps
  working; reloading it is the first connection that uses the new certificate.
- `[UNVERIFIED]` The same from the System page: pick the two files (or paste them), enter the
  password, and the card says **Installed by you**, with the certificate's names, issuer, dates,
  and fingerprint matching what the browser's certificate viewer shows. A wrong password installs
  nothing and the log shows `Failed to install a TLS certificate (wrong password)`.
- `[UNVERIFIED]` Opening the UI by the host's IP address, which the certificate doesn't name, still
  gets the browser's warning, and the card's note said so when it was installed. A certificate that
  covers the hostname, the `.local` name, or an address of the host has no such note.
- `[UNVERIFIED]` A certificate file without the intermediates (just the leaf, from a CA that has
  them) installs with the note about the missing chain, and an Android or iOS browser's behavior
  with it is as the note says (some fetch the intermediates, some don't).
- `[VERIFIED 2026-10-05]` `sudo systemctl restart drawbridge` keeps the installed certificate: the
  journal's `web UI TLS certificate` line says `source=uploaded`. It did on both restarts made that
  day (a stop and a start, and the package's `systemctl restart` during an upgrade), and on the
  three the maintainer made after installing the certificate on 2026-10-04.
- `[UNVERIFIED]` After a restart, the System page still shows the installed certificate.
- `[UNVERIFIED]` A certificate within 30 days of its end makes the System page's TLS check (and
  the dashboard's banner) warn and say to install a renewed one, and an expired one makes it
  fail; the daemon keeps serving it either way, and doesn't swap in the self-signed one.
- `[UNVERIFIED]` certbot's `--deploy-hook` with `drawbridge tls install --cert
  "$RENEWED_LINEAGE/fullchain.pem" --key "$RENEWED_LINEAGE/privkey.pem"` installs the renewed
  certificate after `certbot renew --force-renewal`, and `drawbridge tls show` has the new dates.
- `[UNVERIFIED]` `sudo drawbridge tls reset` (or **Use the Self-Signed Certificate**) goes back:
  the next load warns again, with the fingerprint the setup token printed, and
  `/var/lib/drawbridge/tls/uploaded.pem` is gone.
- `[UNVERIFIED]` With the daemon under its systemd sandbox (`ProtectSystem=strict`), the install
  writes `/var/lib/drawbridge/tls/uploaded.pem` as the `drawbridge` user with mode 0600.
- `[UNVERIFIED]` A damaged `uploaded.pem` (truncate it by hand) doesn't stop the daemon: it starts,
  logs `can't use the uploaded TLS certificate`, and serves the self-signed one until `tls reset`
  or a new install.
- `[VERIFIED 2026-10-05]` A read-only API token gets 403 from all three methods on
  `/api/system/certificate`. On the installed daemon, GET and PUT (with an empty JSON body)
  returned 403. DELETE ran against a scratch daemon with a certificate installed in it, because it
  needs no password, so on the installed daemon a failed gate would have deleted the maintainer's
  own certificate. It returned 403, the scratch certificate stayed installed, and the admin's
  session got 200 from the same DELETE.

## 17. Two-factor authentication (the seventh M5 slice)

TOTP two-factor authentication, the Account page's **Two-Factor Authentication** section, the
login's second step, and `drawbridge admin disable-2fa` (docs/PLAN.md §6.5, docs/two-factor.md).
Nothing here has run on the reference platform, so nothing is `[VERIFIED]` yet. What has run, away
from it: the Go tests check the codes against the RFC 4226 and RFC 6238 test vectors, and the
security rules (a code is good once, a right password forgives no failures, a wrong code is limited
like a wrong password) with a mutation check of each; the browser tests turn it on, log in with
codes and recovery codes, make new codes, turn it off, and use `admin disable-2fa` against the
real daemon (the fake backend), with a code generator written separately from the server's. No
real authenticator app has scanned the QR code, and no phone's clock has been involved.

- `[UNVERIFIED]` With a real authenticator app on a phone (try two of Aegis, Google Authenticator,
  1Password, and Bitwarden), scanning the QR code adds an entry for Drawbridge with the account `admin`, and
  the six-digit code it shows turns 2FA on. Typing the key shown beside the QR code into an app that
  can't scan gives the same codes.
- `[UNVERIFIED]` Logging out and in asks for the code after the password, and the app's code logs
  in. A second login within the same 30 seconds with the same code is refused, and works with the
  next code. A wrong code shows the error and stays on the code step.
- `[UNVERIFIED]` On the reference host, with `systemd-timesyncd` running, the app's codes are
  accepted when the phone's clock is a few seconds fast or slow. With the host's clock set a few
  minutes off (`sudo timedatectl set-ntp false; sudo timedatectl set-time ...`), they're refused.
  Note whether `drawbridge doctor`'s Clock check notices (it reads only timesyncd's synchronized
  flag), and that `sudo timedatectl set-ntp true` fixes it.
- `[UNVERIFIED]` After a reboot (a Raspberry Pi without its RTC battery starts with the wrong time
  until timesyncd sets it), the first login waits for the clock and then works.
- `[UNVERIFIED]` A recovery code logs in once, and the Account page then says nine are left. New
  recovery codes void the old ones.
- `[UNVERIFIED]` `sudo drawbridge admin disable-2fa` over ssh turns it off, logs out the browsers,
  and the log shows `auth.totp_disabled` from the CLI. `sudo drawbridge admin reset-password`
  doesn't turn it off.
- `[UNVERIFIED]` Five wrong codes with the right password make the sixth attempt wait, even with
  the right code, and the wait doubles. `sudo drawbridge admin disable-2fa` lifts it.
- `[UNVERIFIED]` A backup made with 2FA on, restored onto a fresh host (docs/backup-restore.md),
  keeps it on: the same app's codes log in, and an unused recovery code works.
- `[UNVERIFIED]` Upgrading from the previous `.deb` keeps the account and its password, with 2FA
  off; the journal says `upgraded the database` and `backups/` has the snapshot from before
  the migration. The maintainer's own upgrade on 2026-10-04 (not watched) is in the journal
  (`from_schema=10 to_schema=11`, the snapshot `pre-migration-v10-20261004-223428.db`, still in
  `backups/`); the one account is still there, was logged in to with its password the next day,
  and has 2FA off.
- `[VERIFIED 2026-10-05]` A read-only API token (docs/api-tokens.md) still reads
  `/api/server/status` with 2FA on, and gets 403 from all four `/api/auth/totp/*` routes. On the
  installed daemon (2FA off) a token got 403 from all four, with an empty body. On a scratch daemon
  with 2FA turned on through its API (a code made from the enrolled secret by a separate script
  turned it on, and a login with the right password and no code was then refused with
  `totp_required`), the token still read `/api/server/status` (200) and still got 403 from the four
  routes.
- `[UNVERIFIED]` Over the VPN from a phone: the login's code step and the Account page work in the
  phone's browser, and the browser's password manager doesn't fill the code field with the
  password.

## 18. The upgrade matrix (the eighth M5 slice)

The tests that upgrade a host from every older build there is (docs/PLAN.md §12), and a bug they
found (two processes opening an old database at the same time failed, on a snapshot name or a table
that already existed; docs/PLAN.md §7 says what happens now). Two kinds. The data half runs in `go
test` (`internal/store`, `internal/backup`): a database as each schema from 1 to 11 left it, written
by hand, for a host that was used and one that was never set up, upgraded by this build, restored
from an old backup and an old snapshot. The tunnel half runs a real older binary (`make
test-upgrade`, `test/integration/upgrade-from.txt`): it sets a host up with a client connected
through the tunnel and an admin logged in, then this build takes over the way the package does, with
the daemon swapped and nothing else restarted.

What has run, away from the reference platform: all of the data half, and all seven runs of the
tunnel half, in Docker Desktop's VM (a 7.0 aarch64 kernel with WireGuard and IPv6), from builds of
schemas 5, 6, 7, 8, 9, 10, and 11 to this one. In each, wg0 kept its interface index, key, port, and
peers; the 40-odd fetches through the tunnel from a connected client during the swap (the test logs
the count) didn't fail once; every row the older build stored was still there; the admin's login
from before the upgrade still worked, and so did logging in again with the password the older build
printed; the TLS certificate was the same file; and after `tunnel down` and `tunnel up` the client
reconnected with the config it had. A mutation check of each: a migration that loses rows, one that
rewrites a value, and a tunnel restarted during the swap each fail the tests that should.

- `[VERIFIED 2026-10-05]` CI's "Integration" job passes with the new "Upgrade tests" step, on a
  GitHub-hosted runner (Ubuntu's kernel, not Docker Desktop's). Run 37396847671, on main at
  `17e7932`, succeeded in every job. Its step "Upgrade tests (each older build, then this one, with
  the tunnel up)" is `success`, on a GitHub-hosted `ubuntu-24.04` runner (read with `gh run view`).
- `[VERIFIED 2026-10-05]` `make test-upgrade` on the reference platform passes for every build in
  the list. It needs root, the wireguard module, and the whole history (`git fetch --unshallow` on a
  shallow clone), and takes about 25 seconds of test and one build for each. All 7 builds (schemas
  5 to 11) passed at the lowest CPU and IO priority, 21 to 23 s of test each and 3 min 39 s in all
  with the builds, on Linux 6.18 with the real tunnel up beside them. Afterward the host's own
  network was as before: no namespace or worktree left, and the same interfaces, nft tables, `wg0`
  with its peers, sysctls, and files in `/var/lib/drawbridge`.
- `[VERIFIED 2026-10-05]` The package half without a migration: `sudo apt install --reinstall
  ./drawbridge_*.deb` of a build from `17e7932` over `afff457`. (The two packages had the same
  version, so a plain `apt install` says "already the newest version" and does nothing.) apt took
  3.4 s and exited 0, `dpkg --audit` was clean, and `postinst` did what its script says: the daemon
  alone restarted (78 ms from "Drawbridge stopping" to "Drawbridge started" in the journal), the
  tunnel unit was neither stopped nor restarted (the same `InvocationID` and start time), no setup
  token was printed, and the certificate the maintainer had installed was kept. A watcher polling
  ten times a second saw `wg0` at the same interface index with all its peers on every one of 73
  samples, and the web UI's `/healthz` answered 200 on every one (the gap was shorter than the
  sampling). Afterward `server show`, `wg show` (without handshakes and counters), `client list`,
  the nft table's revision, and the secret key's hash were the same as before. Nobody was connected,
  and the two builds had the same schema, so nothing was migrated.
- `[UNVERIFIED]` The same with a client connected, and across a migration: `sudo apt install
  ./drawbridge_*.deb` over the previous release, as in §2's `[VERIFIED 2026-09-27]` step, now also
  with the journal saying `upgraded the database` and `backups/` holding the snapshot (§13's first
  step). The matrix swaps the daemon the way `postinst` does; it doesn't run `postinst`. The
  maintainer's own upgrades on 2026-10-04 (schemas 8 to 9, 9 to 10, and 10 to 11, each with its
  `pre-migration-v<n>-*.db` in `backups/`) are in the journal as `upgraded the database`, and the
  tunnel unit's start time didn't change through them or through the 19 daemon starts since
  2026-10-03, but nobody was connected, and they weren't watched.
- `[VERIFIED 2026-10-06]` A downgrade, on the reference platform: after `sudo apt install
  ./drawbridge_*.deb` of an older build over a newer one (the newer one must have a migration the
  older lacks), the tunnel is up and a connected client stays connected, and `drawbridge.service`
  is `failed` with exit status 78, tried once and not again (`RestartPreventExitStatus=78`), with
  the journal naming both schemas. `apt` succeeds and says "the web UI didn't start; see why with:
  journalctl -u drawbridge" (§19: a daemon that won't restart doesn't fail `postinst`; before
  2026-10-04 it did, and left the package half-configured). Installing the newer build again
  brings the daemon back, with the database untouched. **What the older build was:** no real one
  exists, because the guard arrived (`77b9486`) after schema 11. A build from before it, `3f0314d`
  (schema 10), doesn't refuse: run on a copy of the database in scratch space, it started, and
  the copy changed. So the older build here is this tree's build with its newest migration (0011)
  removed, which has the guard and knows schema 10 only, packaged with this tree's maintainer
  scripts and units at the installed version, so `apt install --reinstall`. Beforehand, in scratch
  space against a copy of the database, it exited 78 in 0.03 s with "the database is from a newer
  Drawbridge: it's at schema 11 and this build knows up to 10; run the Drawbridge that last used
  it, or restore a backup made by this one", and the copy's sha256 didn't change. On the host,
  after a fresh tar with only the daemon stopped: apt exited 0 in 3.5 s, printed systemd's "Job for
  drawbridge.service failed because the control process exited with error code" and then
  "drawbridge: the web UI didn't start; see why with: journalctl -u drawbridge", `dpkg -l` said
  `ii`, and `dpkg --audit` was clean. The journal had the error above, "Main process exited,
  code=exited, status=78/CONFIG", and "Failed to start". The unit stayed `failed` for the 30 s
  watched, with the same invocation and no restart counted. The tunnel unit kept its invocation
  and start time, `wg0` its interface index, the firewall its revision, and the real peers their
  endpoints, handshakes, and byte counters (a digest of them was the same). A throwaway client
  connected from a namespace answered every ping (220 replies, the longest gap 0.21 s at a 0.2 s
  interval). A fingerprint of `/etc/drawbridge` and `/var/lib/drawbridge` (every name, owner, mode,
  size, and sha256) was identical to the one taken before the install: nothing was written, not
  even a `-wal` file or a snapshot. The CLI said "can't reach the Drawbridge daemon at
  /run/drawbridge/control.sock; is drawbridge.service running?". Installing the current package
  again (apt exit 0 in 3.4 s) brought the web UI back 3.5 s later, 41 s after the daemon was
  stopped (30 of them a wait), with `/usr/bin/drawbridge` byte-identical to the original and
  `drawbridge doctor` at 13 of 13. A fresh backup, restored in scratch space, passed the integrity
  check at schema 11, with the real clients' rows, the server's row, and the admin account as they
  were before the older build was installed.
- `[VERIFIED 2026-10-06]` After that downgrade, `journalctl -u drawbridge-tunnel` has the "newer
  Drawbridge" warning, and the older build brings the VPN up. It can't come from the install:
  `postinst` never restarts the tunnel unit, so the older build's `tunnel up` didn't run (the
  unit's journal had no new line). It shows when the unit runs the older build: after a reboot,
  say, or `systemctl restart drawbridge-tunnel`, or `systemctl reload drawbridge-tunnel`, whose
  `ExecReload` is `drawbridge tunnel up`. In scratch space first, the same build's `tunnel up`, as
  root in an empty network namespace against a copy of the database, exited 0 in 0.05 s with the
  warning (`schema=11 known=10`), created `wg0` with the database's peers and its firewall table,
  and left the copy unchanged. **On the host,** the same older-schema build was installed the same
  way (a fresh tar first, only the daemon stopped, and a throwaway client connected from a
  namespace; the daemon failed with exit status 78 again, and the unit's journal had no new line).
  Then `systemctl reload drawbridge-tunnel` exited 0 in 0.09 s, and the unit's journal got
  `level=WARN msg="the database is from a newer Drawbridge than this one: going on, because the
  tunnel only reads it, but the web UI won't start until Drawbridge is upgraded again"
  schema=11 known=10` and `tunnel up ... changes=[]`. The unit kept its invocation and start time,
  `wg0` was never away (a watcher sampling every 50 ms saw it on every sample) and kept its
  interface index, the firewall kept its revision, the peers were as before, and the throwaway
  kept answering (16 replies in the window, the longest gap 0.21 s at a 0.2 s interval). Then
  `systemctl restart drawbridge-tunnel` (`ExecStop` is `tunnel down`, `ExecStart` is `tunnel up`,
  both the older build's) exited 0 in 0.47 s. The journal had the warning twice, once from each of
  the two, and `tunnel up` said it created `wg0`, applied the firewall, set the key and the port,
  added all six peers (the five real clients and the throwaway) and both addresses, and brought
  `wg0` up. `wg0` was away 0.3 s, and came back as a new interface with the same key, port, peers,
  allowed IPs, and keepalive, and the firewall table with the same revision. `drawbridge.service`
  stayed failed (78), with the same invocation. A fingerprint of `/etc/drawbridge` and
  `/var/lib/drawbridge` (every name, owner, mode, size, and sha256) was identical to the one taken
  before the install after the install, after the reload, and after the restart: the older build
  wrote nothing. The throwaway reconnected by itself, with the config it had, 15.3 s after `wg0`
  was back, which fits WireGuard's own timer (a client that sent data and heard nothing for 15 s
  starts a handshake; the handshake wasn't captured). No real client was connected (none had shaken
  hands since `wg0` was last recreated, earlier that day), so a phone's reaction wasn't seen.
  Installing the current package again (apt exit 0 in 3.4 s) brought the web UI back 3.4 s after it
  began, 27 s after the daemon was stopped, with `/usr/bin/drawbridge` byte-identical to the
  original, `drawbridge doctor` at 13 of 13, `client list` and `server show` as before, the web UI
  answering through the VPN from the throwaway's namespace, and a fresh backup, restored in
  scratch space, passing the integrity check at schema 11 with the same rows as before the older
  build was installed. Not seen: a reboot with the older build installed (the same `ExecStart`,
  from a stopped unit).

## 19. The package scripts keep the admin's choices (the ninth M5 slice)

What `postinst`, `prerm`, and `postrm` do to the two units (docs/PLAN.md §11): an admin's
`systemctl disable` survives an upgrade, `remove` keeps the units enabled so a reinstall brings
them back, `purge` deletes their links, and a daemon that won't restart doesn't fail the package.

What has run, away from the reference platform (`make test-packaging`, in a Debian 13 container):
the real scripts through real `dpkg`, against a fake `systemctl`. A first install, an upgrade, a
downgrade, a remove, a reinstall after remove, a purge, an install after purge, and an aborted
upgrade, each with the units enabled, disabled, stopped, and failing to start. Run against the
scripts before this change, it fails 40 checks (a disabled unit enabled again, a failed restart
leaving the package half-configured, `remove` disabling the units), and against the new scripts
without the link deletion, the purge checks fail. What it can't show is systemd itself: the fake
reads "enabled" from the link in `multi-user.target.wants` the way systemd does, but nothing here
has watched systemd.

- `[VERIFIED 2026-10-05]` CI's "Package scripts" job passes, on a GitHub-hosted runner (Ubuntu's
  `dpkg`, not Debian's). In run 37396847671 (main at `17e7932`) the step "Install, upgrade,
  downgrade, remove, and purge" is `success`, on a GitHub-hosted `ubuntu-24.04` runner.
- `[VERIFIED 2026-10-06]` **Disabled across an upgrade:** `sudo systemctl disable --now
  drawbridge`, then `sudo apt install ./drawbridge_*.deb` over the same or a newer build.
  `systemctl is-enabled drawbridge` still says `disabled`, `systemctl is-active drawbridge` says
  `inactive`, and the tunnel is still up with its client connected. The install prints no setup
  token and doesn't pause for the daemon's socket. Then `sudo systemctl enable --now drawbridge`
  brings the web UI back, with its data. Run on the reference host with the current package put on
  over itself (`apt install --reinstall`, the same version) after a fresh tar of the state, with a
  throwaway client connected from a namespace. `disable --now` removed the daemon's boot link
  (`multi-user.target.wants/drawbridge.service`) and left the tunnel unit's. The install exited 0 in
  3.3 s, with no setup token in its output, nothing about the web UI failing to start, and no
  pause (the wait for the daemon's socket is up to 10 s). After it the daemon was still `disabled`
  and `inactive`, `dpkg -l` said `ii`, `dpkg --audit` was clean, and a fingerprint of
  `/etc/drawbridge` and `/var/lib/drawbridge` (every name, owner, mode, size, and sha256) was
  identical to the one taken before it. The tunnel unit kept its invocation and start time, `wg0`
  its interface index, the firewall its revision, and the real peers their endpoints,
  handshakes, and byte counters, and the throwaway answered every ping. `enable --now` made the
  link again, and the web UI answered 0.4 s later (8.3 s after `disable --now`), with `drawbridge
  doctor` at 13 of 13 and the clients and settings as before.
- `[VERIFIED 2026-10-06]` **The tunnel disabled:** `sudo systemctl disable drawbridge-tunnel`
  (without `--now`), then an upgrade. The tunnel unit is neither stopped nor restarted, and is
  still `disabled`; the daemon restarts. Run the same way, after a fresh tar. `disable` removed the
  tunnel unit's boot link and the unit stayed `active` (`--now` would have run `tunnel down`: the
  unit's `ExecStop`). The install exited 0 in 3.4 s. Afterward the tunnel unit was still
  `disabled` and `active`, with the same invocation and start time, `wg0` kept its index, and the
  firewall's revision and the real peers' state were unchanged. The daemon had a new invocation
  and was active (its journal has "Drawbridge stopping" and then "Drawbridge started", and the
  installed certificate still in use), the throwaway answered every ping (89 replies across both
  runs, the longest gap 0.21 s), and the clients were as before. `systemctl enable
  drawbridge-tunnel` (no `--now`) made the link again with the unit untouched.
- `[VERIFIED 2026-10-06]` **Removing and installing again:** `sudo apt remove drawbridge` stops
  both units (`systemctl status` says they can't be found), removes `wg0` and the `inet
  drawbridge` table, and leaves `/etc/systemd/system/multi-user.target.wants/drawbridge*.service`
  (now pointing at nothing). Installing the package again brings both units, `wg0`, and the table
  back with every client and setting, with no new setup token. If the daemon was disabled first,
  it stays disabled after the reinstall. Run twice on the reference host, with a fresh tar of the
  state each time and a throwaway client connected from a namespace. **Plain:** `apt remove -y`
  exited 0 in 3.0 s, `dpkg -l` said `rc`, and `systemctl status drawbridge` and `drawbridge-tunnel`
  each printed "Unit drawbridge.service could not be found." (exit status 4). `wg0` and the table
  were gone, no daemon process or control socket was left, and the binary was gone. Both boot links
  were still there and dangling. `/etc/drawbridge` and `/var/lib/drawbridge` were identical (every
  name, owner, mode, size, and sha256) to before the remove. `apt install -y` of the package again
  exited 0 in 3.1 s with no setup token, `dpkg -l` said `ii`, and both units were enabled and
  active. The web UI answered 3.2 s after the install began, 6.3 s after the remove began, and
  `wg0` had been away 3.7 s. `wg0` was a new interface (a new index) with the same key, port,
  peers, allowed IPs, and keepalive, the firewall table came back with the same revision, the
  binary and the secret key were the original, and the tunnel unit's journal had `tunnel down` and
  then `tunnel up` creating `wg0` and adding all six peers. The clients and settings were as
  before, `drawbridge doctor` passed 13 of 13, and a fresh backup restored in scratch space passed
  the integrity check with the real clients' rows, the server's row, and the admin account as
  before. What doesn't come back is the kernel's per-peer runtime state, which belongs to the
  interface: every real client's handshake went to "never" and its totals to 0 B (one had had
  239 MiB received and 5.0 GiB sent), as in §14. The throwaway reconnected by itself, with the
  config it had, 11.8 s after `wg0` was back. **With the daemon disabled first**
  (`disable --now`): the same remove left only the tunnel unit's link, dangling, since `disable`
  had removed the daemon's. The install exited 0 in 3.1 s with no setup token and no "web UI
  didn't start" message, the daemon stayed `disabled` and `inactive` with no link, the tunnel
  unit was enabled and active, and `wg0` and the table were back as before. `wg0` was away 3.6 s,
  and the throwaway reconnected 11.9 s after it was back. `systemctl enable --now drawbridge` then
  brought the web UI back with its data.
- `[VERIFIED 2026-10-06]` **Purging:** `sudo apt purge drawbridge` also deletes those two links. `ls
  /etc/systemd/system/multi-user.target.wants/ | grep drawbridge` prints nothing. The next
  install is a first install: both units enabled and started, and a setup token printed. Run on
  the real host after a fresh tar, and an encrypted backup that was restored in scratch space
  first: the purge (exit 0 in 3.7 s) removed `/etc/drawbridge`, `/var/lib/drawbridge`, `wg0`, the
  firewall table, and both links, and kept the system user with the same uid. The install (exit 0
  in 3.4 s) printed a setup token, enabled and started both units, made a new secret key, and
  started with no clients. Then, with the daemon stopped, `backup restore` of the backup (exit 0)
  brought back the original key (same sha256), the clients, the server row, and the admin account
  (row digests identical), and the installed certificate and the six snapshots, which a backup
  doesn't hold, were put back from the tar with the right owners and modes. After restarting both
  units the web UI answered 0.5 s later (8.2 s after the purge began), the journal said the
  certificate source was `uploaded`, `server show`, `client list`, `wg show`, and the firewall
  were as before, and `doctor` was at 13 of 13. Not seen: a real client reconnecting.
- `[VERIFIED 2026-10-06]` **A daemon that won't restart:** with the downgrade step above (§18),
  `apt` reports success, and `dpkg -l drawbridge` says `ii`, not `iF`. Run with the older-schema
  build described there: apt exited 0 and printed "drawbridge: the web UI didn't start; see why
  with: journalctl -u drawbridge", `dpkg -l` said `ii`, and `dpkg --audit` printed nothing.
- `[UNVERIFIED]` **A package from before this change, removed:** the older `prerm` disables the
  units on `remove`, so installing this build afterward leaves both disabled (a reinstall goes by
  their state, and they're off). `sudo systemctl enable --now drawbridge-tunnel drawbridge` is the
  fix. Only a host that removed a build from before 2026-10-04 and installs this one sees it.

## 20. Releases (the tenth M5 slice)

`release.yml`, `scripts/install.sh`, and their tests are built (docs/PLAN.md §11.1), and CI runs the
scripts' tests. What only a real tag on GitHub shows can't run until one is pushed, so these are
for the first release. It goes out as a release candidate, `v0.1.0-rc.1`, first: a draft's files
need the maintainer's login, so `install.sh` can't try a draft, and a published release can't be
changed. A pre-release is public and never "latest", which makes it the rehearsal. The draft's own
files are what to install, not a build from the tree.

- `[UNVERIFIED]` A dry run (`release.yml` started by hand on a branch, or on a pull request that
  touches it) passes in Actions: the build, the check of the files, and the second build, whose
  packages match the first's byte for byte on a different machine.
- `[UNVERIFIED]` Pushing the tag runs the whole workflow, `ci.yml` included, and leaves a **draft**
  release (not published, not "latest") with seven files: both packages, their SBOMs, the web
  app's SBOM, `install.sh`, and `SHA256SUMS`, and the notes are the changelog's section and the
  footer.
- `[UNVERIFIED]` GitHub kept every file's name. The workflow's last check fails when it didn't (the
  likely cause is the `~` in a pre-release's file names, as in `drawbridge_0.1.0~rc.1_arm64.deb`,
  which is why the release candidate is the test), and then `install.sh` and `SHA256SUMS` wouldn't
  agree with the release.
- `[UNVERIFIED]` `gh attestation verify drawbridge_<version>_<arch>.deb --repo stuffam/drawbridge`
  succeeds for each package, `install.sh`, and `SHA256SUMS`, and fails for a file that was changed.
- `[UNVERIFIED]` `sha256sum -c --ignore-missing SHA256SUMS` passes for the downloaded files.
- `[UNVERIFIED]` The draft's `.deb`, downloaded with `gh release download`, installs on the reference
  platform the way docs/install.md says (`sudo apt install ./drawbridge_*.deb`), and
  `drawbridge version` reports the tag's version, and the commit.
- `[UNVERIFIED]` The same on an amd64 host (the package has only run in CI on that architecture, never
  on a real machine).
- `[UNVERIFIED]` With `v0.1.0-rc.1` published, `sh install.sh --version v0.1.0-rc.1`, on a host
  without Drawbridge, downloads it from GitHub, checks it, and installs it, with the setup token at
  the end. Run again, it says it's already installed. With no `--version` it says there's no
  release yet, because a pre-release is never "latest".
- `[VERIFIED 2026-10-05]` The two URL shapes `install.sh` relies on, against GitHub itself:
  `/releases/latest` redirects to `/releases/tag/<tag>` on a repository with releases, and to
  `/releases` on one without (this one, then), and `/releases/download/<tag>/<file>` resolves to the
  file.
- `[UNVERIFIED]` After v0.1.0 is published, `curl -fsSL
  https://github.com/stuffam/drawbridge/releases/latest/download/install.sh | sh` asks on the
  terminal (it can't read standard input for it), and picks v0.1.0 and not the release candidate.
- `[UNVERIFIED]` The package's upgrade over the previous release keeps the tunnel up and the data
  (§2 and §18 do it for builds from the tree; this is the same with the released file). The first
  release has nothing before it, so this waits for the second.
- `[UNVERIFIED]` The repository's settings are in place: a tag ruleset on `v*` that blocks updates and
  deletions, and immutable releases, so a published release's files can't be changed.
