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
- `[UNVERIFIED]` `sudo drawbridge server set --dns server` on a host with AdGuard Home listening on
  all addresses saves both VPN addresses; with the resolver stopped it refuses and says nothing
  answers, and `--force` saves them anyway. The tests cover both with a stand-in resolver.
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
  the 3-minute idle wait.
- `[UNVERIFIED]` The Logs page's "Client connections" filter, clicked through a real browser,
  shows the same connect/disconnect/roam events `drawbridge events` does. (Needs a real
  logged-in browser session; see §5.)
- `[UNVERIFIED]` Under the installed units' sandbox (`ProtectSystem=strict`, only `CAP_NET_ADMIN`),
  the daemon's events reach the journal as fields: `sudo journalctl -u drawbridge
  DRAWBRIDGE_CLIENT=<a client's name>` lists that client's events (added, paused, connected,
  disconnected), `-o json` shows `DRAWBRIDGE_EVENT`, `DRAWBRIDGE_CATEGORY`, `DRAWBRIDGE_VIA`, and
  `PRIORITY`, and a wrong password at the login shows under `journalctl -u drawbridge -p warning`.
  Real journald accepts the entries (the integration tests check that against the runner's
  journald, with a fake backend), but the unit's sandbox hasn't been run against it.
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
- `[UNVERIFIED]` The live stream (`GET /api/stream`) on the reference platform, with the dashboard
  open on a laptop: a real phone turning its tunnel on shows as Online within about 6 seconds with
  no touch of the page, `sudo drawbridge client pause NAME` on the host changes the tiles within a
  second, and the Logs page lists a `client.connected` row at the top as it happens. The stream
  and its fallback are tested against a fake backend in a headless browser.
- `[UNVERIFIED]` The stream through the real network and the real daemon: a dashboard left
  visible stays current overnight and stays logged in until twelve hours after login, while a
  hidden tab idles out after an hour. `sudo systemctl restart drawbridge` with a page open
  brings the page back to live by itself within seconds, and `sudo systemctl stop drawbridge`
  with a page open returns at once, not after ten seconds.
- `[UNVERIFIED]` A connected client's session bytes on the dashboard still grow at every 5 second
  refresh although the database gets them once a minute, and after `sudo systemctl restart
  drawbridge` the session carries on (no new `connected` event) and its bytes are right again at
  the first poll.

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
- `[UNVERIFIED]` `sudo drawbridge doctor` against the installed daemon on the reference platform
  (the sandboxed run above used the fake backend, so it didn't see the real `wg0`).
- `[UNVERIFIED]` The System page (the pulse icon in the header) lists the same 13 checks, in the
  same order, with the same results as `sudo drawbridge doctor`, and **Run again** gives a fresh
  set. With nobody connected, `sudo sysctl -w net.ipv4.ip_forward=0` makes the next run show
  Forwarding sysctls as Failed, with the command that turns it back on; running that command
  clears it on the run after.
- `[UNVERIFIED]` On a host with ufw (default forward policy `DROP`), firewalld, or rootful Docker,
  the host firewall check warns, and the printed command (`ufw route allow`, the trusted zone,
  `DOCKER-USER`) clears it. The parser is tested on nft output shaped like each tool's, not on a
  real install of each.
- `[UNVERIFIED]` On an ifupdown host with `accept_ra` 1, the accept-ra check fails with the fix in
  its hint, and the fix clears it.
- `[UNVERIFIED]` An endpoint name whose AAAA record is a temporary address warns.
- `[UNVERIFIED]` A host that keeps time with chrony or ntpd shows the clock warning even when the
  clock is right (a known limit, docs/REQUIREMENTS.md).
- `[UNVERIFIED]` The dashboard raises what the System page shows as Warning or Failed, in one
  banner (red when anything failed, amber otherwise) that names each check and links to the System
  page, and shows no banner when every check passes or is skipped. With nobody connected,
  `sudo sysctl -w net.ipv4.ip_forward=0` makes the banner name Forwarding sysctls within five
  minutes (or at once when any setting is saved, or the page is reloaded); turning it back on
  clears the banner the same way. With the tunnel stopped, the dashboard's own "tunnel is stopped"
  banner appears and the diagnostics banner doesn't name the Tunnel check a second time.

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
  seconds, keeping tags, upstreams, and settings set there. Deleting it deletes its name.
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

## 12. Backup and restore (the second M5 slice)

`drawbridge backup create` makes one encrypted file with the database and the secret key, and
`sudo drawbridge backup restore FILE` puts it back (docs/PLAN.md §6.6, docs/backup-restore.md). The
tests run both against a real SQLite database, a real key, and the real command line. None of it
has run on the reference platform, so nothing here is `[VERIFIED]` yet.

- `[UNVERIFIED]` `sudo drawbridge backup create` against the installed daemon asks for the
  passphrase twice with nothing echoed, and writes a file that only root can read. The daemon
  makes it inside its sandbox (a private `/tmp`, and the key read through the `drawbridge`
  group), so check that it doesn't fail on the key or on the temporary files. Note how long it
  takes, and how big the file is, with a few days of traffic history.
- `[UNVERIFIED]` The event log has `Made a backup`, from the CLI's account.
- `[UNVERIFIED]` **The web download:** on the System page, the Backup card with a wrong password
  shows "the current password is wrong" and saves nothing, and with the right one your browser
  saves `drawbridge-<time>.backup`. Do it through the reverse proxy you use for the UI too (a
  proxy that buffers or limits the body could cut it), and once from the VPN. The card then says
  when the last backup was made, and the event log has `Made a backup` from your account (via the
  web) and `Failed to make a backup (wrong password)` for the wrong one, and neither the password
  nor the passphrase. `sudo drawbridge backup restore` of the file you downloaded (with the
  daemon stopped, as in the exit criterion below) works with its passphrase. Note how long the
  click takes on the Pi, since it makes the snapshot and the encryption before the download
  starts.
- `[UNVERIFIED]` The System page's Snapshots card lists the files in
  `/var/lib/drawbridge/backups/` with the right kind, time, and size, newest first, and has no way
  to download one. With `--snapshot-interval 0`, it says the nightly snapshot is off.
- `[UNVERIFIED]` **The exit criterion:** on a freshly flashed card with Drawbridge installed and
  not set up, `sudo systemctl stop drawbridge.service`, `sudo drawbridge backup restore FILE`,
  and `sudo systemctl restart drawbridge-tunnel.service drawbridge.service` bring the old
  server back: log in with the old account, see every client, and a client that was connected
  before reconnects with the config it already has, without being re-added. `ls -l` shows the
  database as `drawbridge:drawbridge` 0600 and `secret.key` as `root:drawbridge` 0640.
- `[UNVERIFIED]` The same on the host the backup came from, after adding a client: the new
  client is gone, the others work, and the `*.before-restore-*` files are there.
- `[UNVERIFIED]` A wrong passphrase, a file with one byte changed, and a file cut short are each
  refused, and the host's database and key are as they were.
- `[UNVERIFIED]` With the daemon running, `restore` refuses and says to stop it.
- `[UNVERIFIED]` A backup made by the previous release restores onto this one, and the database is
  migrated (the upgrade half of the matrix, until that has its own tests).
- `[UNVERIFIED]` After a restore, the browser tab that was logged in to the old host is logged out,
  and an API token made before the backup still works.
- `[UNVERIFIED]` Nightly snapshots: a minute after the daemon starts on a host with none, one
  appears in `/var/lib/drawbridge/backups/` (0600, in a 0700 directory, owned by `drawbridge`), and
  a restart doesn't make another. With `--snapshot-interval 1m` (a scratch run), a new one comes
  every few minutes and only the newest `--snapshot-keep` stay. (Seen in a container with the fake
  backend: the daemon made one 60 seconds after it started, mode 0600 in a 0700 directory, a restart
  made no second one, and `backup restore` took it. Not on the Pi.)
- `[UNVERIFIED]` An upgrade that adds a migration (the next release that does) leaves a
  `pre-migration-v<n>-*.db` before the schema changes, made by `drawbridge-tunnel.service` or the
  daemon, whichever opened the database first, and the journal says "upgraded the database". With
  the directory made unwritable, the upgrade is refused with a message that says the snapshot
  failed, and the old version of the database is untouched.
- `[UNVERIFIED]` `sudo drawbridge backup restore` of a nightly snapshot, with the daemon stopped,
  asks for no passphrase, keeps the host's key, and brings the clients back as they were when it
  was made; a client added since is gone.

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
  client reads "no record" for its config at first, and none is flagged outdated.
- `[UNVERIFIED]` **Handing out a config sets the baseline:** `drawbridge client show NAME` says
  the config is current, with when it was last handed out, after `client config NAME`, after
  `client qr NAME`, after "Download .conf" in the web UI, and after "Show QR code" there.
- `[UNVERIFIED]` **A server change flags it:** change the MTU (`drawbridge server set --mtu`) and
  the client's row and page get a "Config outdated" badge within a few seconds, the dashboard's
  Outdated tile counts it and opens the list filtered to it, and `client list` says `outdated`.
  Putting the MTU back leaves the client current again, with no new download. Changing the
  endpoint, the DNS servers, and the keepalive does the same; turning client isolation on or off
  does not (the firewall isn't in the config).
- `[UNVERIFIED]` **With a real phone:** import a config in the WireGuard app, change the MTU, and
  the badge appears; scan the new QR code, the badge goes, and the app shows the new MTU.
- `[UNVERIFIED]` **Rotating keys:** on a connected phone, "Rotate keys" on its page (the popup asks
  first; Cancel changes nothing) cuts it off within a handshake interval, shows the new QR code at
  once, and the old config never connects again. Scanning the new code reconnects it. The event
  log has `Rotated a client's keys` from the admin and the new public key, and no secret. The
  client's session closes with a `Disconnected` event, then a new one opens.
- `[UNVERIFIED]` `sudo drawbridge client rotate-keys NAME` asks `[y/N]` on a terminal, refuses
  without `--yes` when its input isn't one, and prints the commands for the new QR code and
  config.
- `[UNVERIFIED]` A read-only API token gets 403 from `POST /api/clients/{id}/rotate-keys`, and
  sees `config_outdated` and the `outdated` count in the client list and the status (they hold no
  secret).

## 14. Safe apply and `drawbridge apply` (the fourth M5 slice)

A settings change that could cut the admin off (the listen port, removing a source from the admin
UI's allowlist) is applied on probation: undone after 60 seconds unless it's kept (docs/PLAN.md
§4.3). Nothing here has run on the reference platform, so nothing is `[VERIFIED]` yet. What has run,
away from it: `TestEndToEnd`'s two new steps pass in network namespaces on a 7.0 aarch64 kernel
(Docker Desktop's VM). With real kernel WireGuard, a `server set --port 51999 --safe` cut the
client off (its fetches through the tunnel failed), the daemon put the port back when the window
(4 seconds there) ran out, and the client was back without being touched. `apply --dry-run` listed
a drifted MTU and left it alone, and `apply` fixed it. And the browser tests drive the Keep, Undo
now, and not-kept paths against the daemon with the fake backend and a 10-second window.

- `[UNVERIFIED]` **From a phone on the VPN, the case it is for:** connect the phone to the VPN,
  open the web UI through it, and change the listen port in Settings. The UI answers once and then
  stops (the phone's tunnel is dead: it still sends to the old port), and the router forwards only
  the old port. Within a minute the daemon puts the port back, the tunnel comes back by itself
  (WireGuard retries within about 15 seconds), and the Settings page shows the old port. The event
  log has `Changed server settings` (with `waiting to be kept: 1m0s`) and then `Undid a settings
  change (not kept in time)`, from Drawbridge.
- `[UNVERIFIED]` **From the LAN:** the same change shows a bar on every page with a countdown.
  **Keep changes** leaves the new port in place past the minute (check `sudo wg show`), and the log
  has `Kept a settings change` from the admin. **Undo now** puts the old port back at once.
- `[UNVERIFIED]` The bar is readable on a phone and in dark mode, and its countdown is right when
  the browser's clock is wrong by a few minutes (set one wrong to try).
- `[UNVERIFIED]` **A reboot inside the window:** change the port from the LAN, don't keep it, and
  `sudo reboot` straight away. After the boot the tunnel has the new port for a moment
  (`drawbridge-tunnel.service` applies what the database says), and then the daemon undoes it: the
  port is back to the old one, and `server show` has nothing waiting.
- `[UNVERIFIED]` **Restarting the daemon:** `sudo systemctl restart drawbridge.service` inside the
  window leaves the change waiting, with the countdown carrying on from where it was.
- `[UNVERIFIED]` Removing the source the browser is on from the admin UI's allowlist
  (`sudo drawbridge server set --admin-allow none --safe`, then reload from that source) locks the
  browser out; a minute later the source works again, with nothing done.
- `[UNVERIFIED]` While a change waits, another settings change from the web UI or `server set` is
  refused and says to keep or undo the first, and adding, pausing, and deleting clients still work.
- `[UNVERIFIED]` `sudo drawbridge server show` lists a waiting change, with who made it and the
  seconds left; `sudo drawbridge server confirm` and `revert` work from a terminal, and from the
  web UI's bar when the change was made with `--safe`.
- `[UNVERIFIED]` `sudo drawbridge apply --dry-run` after `sudo ip link set wg0 mtu 1500` (and
  before the daemon's 30-second check notices) lists the MTU and changes nothing; `sudo
  drawbridge apply` puts it back. With the tunnel stopped, both say so and start nothing.
- `[UNVERIFIED]` An upgrade from the previous release (schema 9) takes `pre-migration-v9-*.db`
  and adds the `pending_apply` table; nothing is waiting afterward.

## 15. Rotating the server's key (the fifth M5 slice)

`drawbridge server rotate-key`, the **Rotate the key…** button in Settings, and
`POST /api/server/rotate-key` give the server a new key pair (docs/PLAN.md §6.2). Every client's
config holds the old public key, so every client stops until it imports its new config. The web UI
always puts the rotation on safe apply (§14 above). Nothing here has run on the reference
platform, so nothing is `[VERIFIED]` yet. What has run, away from it: `TestEndToEnd`'s two new steps
pass in network namespaces on a 7.0 aarch64 kernel (Docker Desktop's VM). With real kernel
WireGuard, after `server rotate-key` the kernel had the new private key and the same peer, a
connected client's fetches through the tunnel failed at once (WireGuard drops the current sessions
when the interface's key changes), and the new config connected over IPv4 and IPv6. After
`rotate-key --safe` with nothing kept, the daemon put the old key back when the window (4 seconds
there) ran out, and the client reconnected with the config it already had. And the browser tests
drive the dialog, Undo now, and Keep against the daemon with the fake backend.

- `[UNVERIFIED]` **From the LAN, with a phone on the VPN:** in Settings, **Rotate the key…** asks
  first (Cancel changes nothing), and **Rotate the key** shows the bar with both public keys. The
  phone's tunnel stops carrying traffic at once. Keep it, and `sudo wg show` has the new public
  key; the phone stays off until it imports its new config (the client's page shows **Config
  outdated**, and its QR code is the new one).
- `[UNVERIFIED]` **The case it is for:** with the web UI open through the VPN on a phone, rotate the
  key. The UI stops answering (the phone's tunnel is dead). Within a minute the daemon puts the old
  key back, the tunnel comes back by itself within about 15 seconds, and the phone's old config
  works again without being touched. The log has `Rotated the server's key` and then `Undid a
  settings change (not kept in time)`, from Drawbridge.
- `[UNVERIFIED]` After a kept rotation, the dashboard's Outdated count is the number of clients
  that had been handed a config, and `sudo drawbridge client list` shows the same. A client that
  was never handed a config isn't flagged. Importing each new config clears its flag.
- `[UNVERIFIED]` `sudo drawbridge server rotate-key` asks `[y/N]` on a terminal and refuses without
  one unless given `--yes`; without `--safe` it applies at once and waits for nothing, and with it
  `server confirm` and `server revert` work. While a rotation waits, another settings change is
  refused.
- `[UNVERIFIED]` A read-only API token gets 403 from `POST /api/server/rotate-key`.
- `[UNVERIFIED]` A restart or a reboot inside the window undoes a rotation like any other
  change on probation: the old key is back afterward, and `sudo wg show` agrees.
- `[UNVERIFIED]` A backup made before a rotation holds the old key. Restoring it onto a fresh host
  brings the old key back, and the clients' configs from before the rotation work again.

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
- `[UNVERIFIED]` `sudo systemctl restart drawbridge` keeps the installed certificate: the journal's
  `web UI TLS certificate` line says `source=uploaded`, and the System page still shows it.
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
- `[UNVERIFIED]` A read-only API token gets 403 from all three methods on
  `/api/system/certificate`.

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
  the migration.
- `[UNVERIFIED]` A read-only API token (docs/api-tokens.md) still reads `/api/server/status` with
  2FA on, and gets 403 from all four `/api/auth/totp/*` routes.
- `[UNVERIFIED]` Over the VPN from a phone: the login's code step and the Account page work in the
  phone's browser, and the browser's password manager doesn't fill the code field with the
  password.
