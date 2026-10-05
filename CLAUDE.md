# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this
repository.

## What this is

Drawbridge is a self-hosted web manager for a WireGuard VPN server. It installs natively (a
`.deb`, no Docker) on Debian-family Linux with systemd, for arm64 and amd64, and it manages:

- Clients: add, remove, pause, and view their logs.
- Server settings: FQDN, addresses, and MTU.
- DNS.

It supports both IPv4 and IPv6. wg-easy is the feature reference; none of its code is used.

**Status: M0–M4 are built (the session tracker and traffic history shipped ahead of the rest of
M4, 2026-09-26–28, and the AdGuard Home integration finished it, 2026-10-03), and
`drawbridge doctor` from M5 followed (2026-09-29). Nearly
every check in docs/MANUAL_CHECKLIST.md has passed on the reference platform (a Raspberry Pi 5
running Debian 13); what's left there needs a laptop, a second Tailscale device, or a few days of
traffic history.
docs/REQUIREMENTS.md lists what a host and network need, and the known roadblocks on other
setups.** What exists:

- The tunnel: `drawbridge tunnel up|down`, run by `drawbridge-tunnel.service`.
- The daemon's reconciler and drift loop, and its connection tracker
  (`internal/service/conntrack.go`, docs/PLAN.md §6.4): `connected`/`disconnected`/`roamed`
  events and a `client_sessions` table, built ahead of the rest of M4.
- The traffic-history sampler and rollup/retention job (`internal/service/traffic.go`,
  `internal/store/traffic.go`, docs/PLAN.md §6.4), storing and pruning per-client RX/TX history
  server-side; the API routes that read it and a client's session history
  (`GET /api/clients/{id}/traffic`, `GET /api/traffic`, `GET /api/clients/{id}/sessions`); and
  the uPlot charts and session-history list that use them, on the dashboard and a client's
  detail page. The charts' ranges run from 1 minute to 90 days; the 1-minute range is served
  from the last two minutes of 5 s polls kept in memory (never written to the database), because
  the stored buckets are a minute wide. A Charts page (the chart-line icon in the header, from
  `GET /api/traffic/clients`) draws Received, Sent, and cumulative charts with a line per
  client, and the dashboard and a client's page chart the total or the client's own. One range
  choice covers every chart (`web/src/lib/range.svelte.ts`). The log viewer filters by category,
  event, client, and time, and exports the matching events as CSV. Under systemd every event is
  also a journal entry whose parts are fields (`DRAWBRIDGE_EVENT`, `DRAWBRIDGE_CLIENT`, and so
  on; `internal/journal`). A write-budget test keeps the database
  within what an SD card can take. The dashboard, client pages, and log get their status and
  new events from a Server-Sent Events stream (`GET /api/stream`, `internal/api/stream.go`,
  `web/src/lib/live.svelte.ts`), and poll only when it can't be had. Also ahead of the rest of M4
  (AdGuard Home integration: the API client `internal/adguard`, the connection saved and tested
  from Settings, client name sync, `internal/service/adguardsync.go`, and a client's DNS log,
  `internal/service/dnslog.go`, are all built).
- The CLI, which talks to the daemon over the control socket:
  `server show|set|rotate-key|confirm|revert`,
  `client list|add|show|pause|resume|rename|delete|config|qr|rotate-keys`, `events`, `doctor`,
  `tls show|install|reset`, and `admin setup-token|create|reset-password|disable-2fa`.
- `drawbridge doctor`, the first slice of M5 (2026-09-29): 13 host and network checks, each with
  a fix hint, run by the daemon (`internal/diag`) and printed by the CLI. Exit status 1 when any
  check fails, 0 otherwise. The System page (the pulse icon in the header,
  `GET /api/system/health`) shows the same checks. The dashboard raises the ones that warn or
  fail in a banner that links to the System page (2026-10-04): it asks when it opens, every five
  minutes while it's showing, and when the settings change, and it leaves out the tunnel check and
  an unset endpoint, which the dashboard already says itself from fresher data. The comparison of
  the endpoint's A record with the current public IPv4 address isn't built (docs/PLAN.md §16).
- `drawbridge backup create|restore` (2026-10-03), the second slice of M5: one file with a
  consistent snapshot of the database and the secret key, encrypted with a required passphrase
  (`internal/backup`, docs/backup-restore.md). `create` goes through the daemon; `restore` is
  root-only with the daemon stopped, checks everything before it changes anything, and keeps
  what it replaces. The host also keeps snapshots of the database alone (`internal/snapshot`, in
  `backups/` beside it): a nightly one, and one before a migration, which `restore` takes too. The
  System page makes the same file (it takes the password again and the passphrase twice) and lists
  the snapshots, which it never offers for download.
- Outdated-config tracking and client key rotation (2026-10-04), the third slice of M5. Handing
  out a client's config (a download, a QR code, `client config`) stores its fingerprint
  (`clientconf.Fingerprint`, docs/PLAN.md §6.1); a client whose current fingerprint differs is
  flagged `config_outdated` in the list, on its page, in the dashboard's Outdated count, and in
  `client list`. `client rotate-keys` (and `POST /api/clients/{id}/rotate-keys`, and a button on
  the client's page) gives a client new keys, which cuts the old config off at once.
- Safe apply (2026-10-04), the fourth slice of M5 (docs/PLAN.md §4.3). A settings change that could
  cut the admin off (the listen port, removing an admin-UI source, rotating the server's key) is
  applied at once from the web UI and undone after 60 s unless it's kept, by a bar on every page
  (**Keep changes** / **Undo now**). The held change is in the database (`pending_apply`), so a
  restart or a reboot undoes it too. `drawbridge server set --safe`, `server confirm`, and
  `server revert` do the same from the CLI, which otherwise applies at once; `drawbridge apply
  [--dry-run]` reconciles once and lists what changed (or would).
- Rotating the server's key (2026-10-04), the fifth slice of M5 (docs/PLAN.md §6.2).
  `drawbridge server rotate-key [--safe] [--yes]`, `POST /api/server/rotate-key`, and a **Rotate
  the key…** button in Settings give the server a new key pair. Every client's config holds the
  old public key, so every client stops until it has its new config (the ones that were handed a
  config show as outdated). The web UI always puts it on safe apply, because the admin on the VPN
  is cut off by it.
- Your own TLS certificate (2026-10-04), the sixth slice of M5 (docs/PLAN.md §6.6,
  docs/tls-certificate.md). `drawbridge tls install --cert FILE --key FILE`, `PUT
  /api/system/certificate`, and the System page's **Web UI Certificate** card serve a certificate
  the admin brings instead of the self-signed one, checked first (`tlscert.Parse`) and used by the
  next connection with no restart (`tlscert.Store` is the TLS config's `GetCertificate`). `tls
  reset` and `DELETE` go back. The web path asks for the password again.
- TOTP two-factor authentication (2026-10-04), the seventh slice of M5 (docs/PLAN.md §6.5,
  docs/two-factor.md). The Account page's **Two-Factor Authentication** section turns it on (the
  password again, a QR code, and the first code from the admin's app), shows ten single-use
  recovery codes once, makes new ones, and turns it off (`POST /api/auth/totp/enroll|verify|disable|
  recovery-codes`, `internal/service/totp.go`, `internal/auth/totp.go`, `internal/store/totp.go`).
  With it on, `POST /api/auth/login` answers a right password and no code with a 401 whose error
  code is `totp_required`, and the login page asks for the code in a second step.
  `drawbridge admin disable-2fa` is the way back for an admin who lost the app and the codes.
- The upgrade matrix (2026-10-04), the eighth slice of M5 (docs/PLAN.md §12). Its data half runs in
  `go test`: a database as each schema from 1 to 11 left it, for a used host and one never set up
  (`internal/store/storetest`), is opened by this build and restored from an old backup and an old
  snapshot (`TestUpgradeFromEverySchema`, `TestRestoreFromEverySchema`). Its tunnel half, `make
  test-upgrade`, sets a host up with a real older binary built from each ref in
  `test/integration/upgrade-from.txt`, with a client connected, and swaps in this build the way the
  package does. It found that two processes opening an old database at once failed; `store.Open`
  now re-reads the version inside each migration's transaction. The same slice settled downgrades:
  an older build reads a newer database and never writes it, so the daemon refuses to start on one
  and the tunnel unit carries on (docs/PLAN.md §11).
- The package scripts keep an admin's choices (2026-10-04), the ninth slice of M5 (docs/PLAN.md
  §11). `postinst` enables the units only on a first install and otherwise goes by their state:
  a unit that isn't enabled is left alone, an enabled one has the tunnel started (never
  restarted) and the daemon restarted, so an admin's `systemctl disable` survives an upgrade.
  `remove` stops the units and leaves them enabled, `purge` deletes their links, and a daemon
  that won't restart no longer fails `postinst`. `make test-packaging` runs the real scripts
  through real `dpkg` against a fake `systemctl`.
  Left in M5: the docs (install, router setup, troubleshooting).
- The authenticated JSON API over HTTPS on port 51821 (`internal/api/openapi.json`): first-run
  setup, sessions, server settings, clients, and the event log, reachable only from the LAN and
  the VPN. A dashboard that can't log in (Homepage) reads the status with a read-only API token,
  made on the Account page (docs/api-tokens.md).
- The web UI: setup (account, endpoint, and a DNS step that checks whether the host answers DNS
  on the VPN addresses), login, dashboard, clients (QR codes and downloads), settings (DNS
  included), logs, and account.
- Kernel integration tests, and browser end-to-end tests.

`docs/PLAN.md` is the specification. It covers:

- Architecture and the networking design.
- The feature spec, data model, and API.
- The security design.
- Milestones M0–M6, each with exit criteria.
- The project's decisions (§3, §16).

Read it before writing code here. It describes the target, not what exists. Don't assume
something is built because the plan describes it.

When a decision changes, update `docs/PLAN.md` (§3 and §16) in the same PR. When a command,
convention, or invariant changes, update this file too.

## Usually no real host here

**Check before assuming, in both directions.** Development usually happens away from a real
host, in a cloud container or on a laptop. Anything that needs a real network can't be exercised
there: the router, the public endpoint, a DNS resolver on the host, and real clients.

The cloud container was checked on 2026-09-26 (see "Verified facts"):

- It has root, network namespaces, nftables, and TUN devices.
- It has **no kernel WireGuard**, and its CPU is x86_64.

CI runs on GitHub-hosted `ubuntu-latest` (x86_64) and cross-compiles for arm64. The kernel
WireGuard integration tests (`test/integration`, the "Integration" job) run on a GitHub-hosted
`ubuntu-24.04` VM, which has root through sudo and Ubuntu's kernel. The job loads the kernel
modules first, and the tests pass there (first on 2026-09-26). The tests work only inside
network namespaces they create. `test/integration/preflight.sh` checks every requirement at once
before the tests run.

Keep the Integration job on GitHub-hosted runners. A self-hosted runner (docs/MANUAL_CHECKLIST.md
§4 lists what one needs) must never be attached to this repo while it's public: it would run code
from forks' pull requests.

If a session does have kernel WireGuard or arm64, use it: an observed answer beats a documented
one. Say plainly when a claim is documented but not yet exercised, rather than observed on real
hardware.

`docs/MANUAL_CHECKLIST.md` is the authoritative record of what has actually run on real
hardware (the reference platform in docs/PLAN.md §2). Its
`[UNVERIFIED]`, `[VERIFIED …]`, and `[NEXT]` tags matter; keep them current when you touch the code
a step describes.

## Conventions

- **U.S. English only.** This covers code, comments, docs, UI strings, log messages, and commit
  messages: "color," "canceled," and "behavior," never the British forms. `make spell` (misspell
  with `-locale US`) enforces it on every tracked file, and CI runs it, so don't quote a British
  spelling even as an example. "WireGuard," "AdGuard Home," and "Raspberry Pi" are product
  names and are correct as-is.
- **The repo is public, so nothing in it is personal.** Write for anyone running Drawbridge:
  "the admin" (whoever runs it), "the maintainer" (project decisions), "the host" (the machine
  it runs on). No home-lab specifics: no router models, hostnames, FQDNs, real LAN subnets,
  Tailscale or public addresses, or personal names in fixtures. Use documentation ranges
  (`192.0.2.0/24` and friends, `2001:db8::/32`), generic private ones (`192.168.4.0/22`,
  `100.64.10.0/24`), and `example.com`. "Raspberry Pi 5" and "Debian 13" appear only as the
  tested reference platform. When a feature assumes something about the host or network, say so
  in docs/REQUIREMENTS.md.
- **Version numbers are prefixed with a lowercase `v`** in prose, git tags, release names, and
  `CHANGELOG.md` headings: `v1.0.0`, never `1.0.0`. Three places use a bare string because their
  own rules require it:
  - The Debian package version, which must start with a digit (`drawbridge_1.0.0_arm64.deb`).
  - The version constant set at build time.
  - The OpenAPI spec's `info.version`.
- **Licensed under Apache-2.0** (`LICENSE`, also shipped as `/usr/share/doc/drawbridge/copyright`
  in the `.deb`). No copyright or license headers in individual files.
- **Names:** the product is "Drawbridge" in prose. Everything else uses `drawbridge`:
  - The repository, Go module, and binary.
  - The systemd units (`drawbridge.service`, `drawbridge-tunnel.service`) and the system user.
  - The paths (`/etc/drawbridge`, `/var/lib/drawbridge`, `/run/drawbridge`).
  - The nftables table (`inet drawbridge`) and the journald field (`DRAWBRIDGE_CLIENT=`).

  "WireGuard" is a registered trademark. It appears only descriptively ("a web manager for
  WireGuard"), never in the product name.
- **Dependencies are deliberate.** (`golang.org/x/term` was added for the backup passphrase's
  no-echo prompt, on the argument that reading a passphrase with the echo on is not an option and
  hand-rolling termios for each platform is worse.) The plan names the core libraries: `wgctrl`,
  `vishvananda/netlink`, and `modernc.org/sqlite` in §3, Tailwind, uPlot, and `qrcode` in §9, and
  Playwright in §12.
  Anything else needs an explicit argument in its PR, and the default answer is no. Everything is
  pure Go (`CGO_ENABLED=0`), so the arm64 build stays a plain cross-compile.
- **When adding behavior, add a test.** This project is most exposed to quiet failures: a change
  that reports success while the kernel disagrees. Examples:
  - A paused client still listed in `wg show`.
  - A rendered nftables rule that was never applied.
  - IPv6 that silently doesn't forward.

  A test that checks only the DB or the rendered text isn't a test of the effect.
- **A test that freezes the service's clock and logs a browser in starts it at the real time.** The
  session cookie expires 12 hours after the service's now, and the cookie jar compares that with the
  real clock, so a fixed date more than 12 hours back drops the cookie and every later request is a
  401. `TestTwoFactorFlow` did that and began failing, on every run, at midnight UTC after the date
  it started at. Fixed dates are fine for data (an event's time, a snapshot's name).
- **Times in the web UI are on a 24-hour clock, and dates are `9 Sep`.** Use `formatClock`,
  `formatDay`, `formatTime`, and `formatChartTime` in `web/src/lib/format.ts`, never
  `toLocaleString` or its relatives, which follow the browser's locale (am and pm, month first).
- **Formatting:** `gofmt` and `goimports` for Go, and Prettier for the web app. Markdown wraps at
  100 columns.

## Commands

Everything goes through the Makefile, and `make help` lists the targets. The first lint, spelling,
or package run installs the pinned tools (golangci-lint, nfpm, misspell) into `./bin`.

```bash
make check      # everything CI checks: Go and web linting, spelling, and all tests
make test-go    # Go tests, with the race detector
make test-web   # the web app's unit tests (Vitest)
make lint       # golangci-lint; Prettier, ESLint, and svelte-check; misspell
make spell      # the U.S. English check on every tracked file
make fmt        # format Go (gofmt, goimports) and the web app (Prettier)
make deb        # web build, arm64 and amd64 binaries, and both .deb files in dist/
make test-integration   # kernel WireGuard end to end (root, IPv6, the wireguard module)
make test-upgrade       # upgrade from each older build (test/integration/upgrade-from.txt)
make test-packaging     # the .deb's scripts through real dpkg, fake systemctl (a throwaway Debian)
make test-e2e   # the web app in Chromium against `serve --backend fake` (Playwright)
```

The integration tests run the built binary: `tunnel up` and `serve` in a server namespace, the
CLI against its control socket, and a client namespace that configures WireGuard from
`client config` and checks NAT44 and NAT66 through an "internet" namespace. Without
`DRAWBRIDGE_INTEGRATION=1` they skip, so they never fail silently on a machine that can't run
them; the Makefile target and CI set it.

One Go test, or one web test file:

```bash
go test ./internal/api -run TestAppFallsBackToShell -v
cd web && npx vitest run src/lib/version.spec.ts
```

`make test-packaging` installs and purges a package named drawbridge, so it refuses to run unless
`DRAWBRIDGE_PACKAGING_TEST=1` (the target sets it) and on a host that has Drawbridge already. Run
it in a throwaway Debian, which is also how to run it without one:

```bash
docker run --rm -v "$PWD":/work:ro -e DRAWBRIDGE_PACKAGING_TEST=1 debian:13-slim \
  /work/test/packaging/test.sh
```

The end-to-end tests start `drawbridge serve --backend fake` on port 51899 (not 51821, so a real
daemon on the same machine is left alone) with a fresh state directory, and use the CLI for the
setup token. Where Playwright can't download its own Chromium, point it at one:
`PLAYWRIGHT_CHROMIUM_PATH=/opt/pw-browsers/chromium-1194/chrome-linux/chrome make test-e2e` in the
cloud container.

Run it locally. Without `make web` first, the embedded UI isn't there and `/` says so.
`--backend fake` keeps the tunnel and the firewall in memory, so the UI works without root or
WireGuard:

```bash
make web && go run ./cmd/drawbridge serve --backend fake --db /tmp/db.sqlite \
  --secret-key /tmp/secret.key --control /tmp/control.sock \
  --listen 127.0.0.1:51821   # https://127.0.0.1:51821
cd web && npm run dev          # Vite on port 5173, proxying /api to https://127.0.0.1:51821
go run ./cmd/drawbridge admin setup-token --control /tmp/control.sock
```

`serve` and `tunnel` need a database, a secret key, and `CAP_NET_ADMIN`. To try them without
installing the package, point them at scratch files: `--db /tmp/db.sqlite --secret-key
/tmp/secret.key` (32 random bytes, mode 0600), and `--control /tmp/control.sock` for `serve` and
every CLI command.
`serve` keeps its self-signed certificate in `tls/` next to the database (`--tls-dir`).
Without `CAP_NET_ADMIN`, or without kernel WireGuard, they report why and keep the database
consistent.

`go build ./cmd/...` at the repository root writes a `drawbridge` binary there, because the
pattern matches one main package. Use `make build` (which writes `dist/`) or `-o`, and never
commit the binary.

**Use npm 11 (Node 24).** npm 10.9.7, the npm bundled with Node 22, fails `npm install` in `web/`
with "Cannot read properties of null (reading 'edgesOut')". CI uses Node 24. On Node 22, pass
`NPM="npx -y npm@11"` to make.

## Invariants (condensed; see docs/PLAN.md for the full design)

These are the rules most likely to get silently broken.

- **The database is the source of truth, and the kernel state is derived from it.**
  - Every change goes: validate → DB transaction → reconcile → event (§4.3).
  - Nothing outside the reconciler touches kernel state (`wgctrl`, netlink, `nft`).
  - Only `drawbridge tunnel up` creates the interface. The daemon's reconcile (`Sync`) leaves
    a missing interface alone, so it never restarts a tunnel the admin stopped (ADR 0008).
  - The CLI changes state only through the daemon's control socket. It never opens the
    live database, which belongs to the `drawbridge` user. The one exception is
    `backup restore`, which runs as root with the daemon stopped, opens only a temporary copy
    of the restored database, and then replaces the live one.
  - The reconciler is idempotent and serialized by a lock. It also runs every 30 s to correct
    drift.
- **Live changes never disrupt other clients.** Peers are diffed and updated in place
  (`ReplaceAllowedIPs`). The interface is never recreated to apply a change.
- **No root at runtime.** Both units run as the `drawbridge` user with only `CAP_NET_ADMIN`
  (§4.2).
  - Anything that needs root (sysctls, the NetworkManager drop-in, the system user) belongs in
    the package's `postinst`, not in the daemon.
  - The only external command is `nft -f`, called with an argv list. Never use a shell.
- **No arbitrary command hooks** (`PostUp`/`PostDown` or anything like them). Anything a hook
  would do is a typed, validated setting instead (§10).
- **A client's "config outdated" flag is computed, not stored, and holds no secret.** The
  fingerprint (`clientconf.Fingerprint`) hashes the config with the client's public key where its
  private key goes, so it can sit unsealed in the database and be recomputed on every read
  without a write. Changing how a config is rendered changes every fingerprint and flags every
  client that was handed one, so `TestFingerprintIsTheHashOfAKnownText` pins it: update it on
  purpose, and say so in the release notes.
- **A web settings change that could lock the admin out is on probation, in the database.**
  `needsConfirmation` (`internal/service/safeapply.go`) decides what waits, and the change and the
  settings it replaced are written in one transaction (`store.UpdateSettingsWith`), so there is
  never a change waiting without a way back. While one waits, every other settings change is
  refused, because undoing restores the whole snapshot. A keep that arrives after the deadline
  undoes the change instead. `settingsSnapshot` lists the settings' fields by hand (the server's
  key is sealed apart, never in the JSON): a field added to `model.Settings` must be added there,
  and `TestSnapshotCarriesEveryField` fails until it is. Something that can lock the admin out and
  isn't on that list applies at once and can't be undone, so a new setting like that goes on it.
  The server's key is on it (`Service.RotateServerKey`). A change's description
  (`settingsChanges`, which the event log and the web bar show) carries the server's public key
  and never the private one.
- **An installed TLS certificate is never replaced unasked, and its key stays out of sight.**
  `tlscert.Store` serves the admin's certificate (`tls/uploaded.pem`, mode 0600) when it loads,
  even after it expires: the doctor warns and fails, and the admin renews it (nothing does). A
  file that can't be loaded is logged and left alone while the self-signed certificate serves, so
  the web UI stays reachable. The key is never in a view, an event, a log line, or a response
  (`TestInstallCertificate` and `TestCertificateOverTheAPI` check), and the web install asks for
  the password again (`TestInstallCertificateForNeedsThePassword`). Neither the key nor the
  certificate is in a backup.
- **Free text never reaches a rendered file.**
  - Client names and notes stay out of nftables rulesets and WireGuard configs.
  - Keys, CIDRs, ports, and MTUs are parsed and validated (`net/netip` and range checks) before
    they're rendered.
- **The VPN doesn't depend on the UI.** `drawbridge-tunnel.service` brings the tunnel up at boot
  from the DB on its own. If `drawbridge.service` stops or crashes, the VPN keeps running.
- **The package scripts keep what the admin chose, and decide on state, never on `$2`.** dpkg
  calls `postinst configure <the version configured last>` for an upgrade, a downgrade, and a
  reinstall after `remove` alike, and only a first install (or one after `purge`) has no `$2`. So
  `postinst` enables the units only when `$2` is empty, and otherwise goes by `systemctl
  is-enabled`: a unit that isn't enabled is left completely alone, the tunnel of an enabled one is
  started and never restarted, and the daemon is restarted. `prerm remove` stops the units and
  never disables them, so a reinstall finds them enabled; `postrm purge` deletes their links.
  `postinst` acts only on `configure`, and a daemon that won't start prints how to see why and
  doesn't fail the script (`set -e` would leave dpkg with a half-configured package, which the
  downgrade guard's exit 78 makes reachable). `test/packaging/test.sh` checks each of these, and
  a change to a script has to keep it passing.
- **Drawbridge owns only `table inet drawbridge`.** Never `flush ruleset` or touch another table.
  Only restrictive rules and NAT go in it, because an `accept` in one table can't override a
  `drop` in another (§5.3).
- **The admin UI is reachable from the home network and the VPN only, never the internet**
  (D11). Two layers enforce this, and both use `lan.Allowlist`:
  - The app's allowlist (`internal/api`, 403): the detected LAN prefixes, loopback, link-local
    addresses, and the VPN subnets. It doesn't trust "private ranges," because LAN devices reach
    the host over the global IPv6 prefix.
  - The nftables `input` chain, which drops TCP 51821 from everyone else. It's part of
    Drawbridge's table, so `tunnel down` removes it too.

  The admin's extra sources are the `admin_allowed` setting (`server set --admin-allow`, for
  Tailscale, say), which both layers read. Each prefix is validated to lie in a private range,
  100.64.0.0/10, or fc00::/7, so no setting can admit a public source.

  Only the TCP connection's address counts. Never trust `X-Forwarded-For` or similar headers.
  Never serve the UI through a reverse proxy on the same host: its requests arrive from the host
  itself and would pass both layers (§6.5).
- **Every change is an event.** Service methods that change something call `record` with the
  actor from the context (the web user, the CLI's account from the socket's peer credentials,
  or the daemon). A new change needs its event, and a test that checks it. `record` also logs the
  event for the journal, so a change doesn't get a log line of its own as well.
- **The API and its OpenAPI document agree.** Routes live only in the `routes` table in
  `internal/api/api.go`, and a test fails when it and `openapi.json` differ. Everything except
  the setup and login endpoints needs a session; every change needs the `X-Drawbridge` header.
- **The web app talks to the server only through `web/src/lib/api.ts`**, which adds the CSRF
  header and sends a lapsed session to the login. Internal links and `goto` go through
  SvelteKit's `resolve()` (ESLint enforces it); route IDs with parameters include the group:
  `resolve('/(app)/clients/[id]', { id })`.
- **The CSP stays strict.** `script-src` allows `'self'` and the app shell's bootstrap script by
  hash, nothing else. Don't add `'unsafe-inline'` or `'unsafe-eval'` to it; change the UI instead.
- **The two address families are kept in lockstep.** Every networking feature is built and
  tested for both IPv4 and IPv6: addressing, NAT, firewall rules, allowlists, DNS, diagnostics,
  and tests. The quiet failure here is a feature that works on IPv4 and does nothing on IPv6.
- **AdGuard Home is optional at runtime.** VPN management keeps working when it's down, and name
  sync retries in the background (§6.3).
- **Don't default to common ports.** Drawbridge uses only UDP 51820 (WireGuard) and TCP 51821
  (admin UI), because hosts often run other services on the usual ones: a DNS resolver on 53,
  web servers and reverse proxies on 80 and 443, and admin UIs like AdGuard Home's on 3000.
  Never default anything to one of those. That includes dev servers and test fixtures that
  might run on a real host. The planned AdGuard Home integration (M4) reaches its API at
  `http://127.0.0.1:3000/control` by default; that address is a setting (`adguard.base_url`,
  §7), not hard-coded.
- **The HTTP server has no write timeout**, because one would cut the event stream (ADR 0009). A
  handler that stays open sets a deadline on each write (`http.ResponseController`) and ends when
  `Options.Shutdown` closes, or the daemon's graceful shutdown would wait on it. A connection that
  has sent no request is closed at shutdown too (`freshConns`, `cmd/drawbridge/serve.go`): net/http's
  `Shutdown` counts it as busy for its first five seconds, and a browser's spare connection would
  make `systemctl stop` take that long.
- **Protect the SD card.** Monitoring samples, and the bytes of open sessions, are buffered in
  memory and flushed once per raw interval (a minute by default) in one transaction. Nothing
  writes to the DB on every poll (§6.4). `TestWriteBudget` simulates a day against a real
  database and fails when the writes pass the budget, so a poll-time write shows up there; keep
  new periodic work out of the poll, or in the flush. `make test-go` runs it without the race
  detector, which makes it too slow.
- **A saved password goes only where it was saved to.** The AdGuard Home password is sent to
  the address and account saved with it. A request that changes either (a save, or a test of
  unsaved values) must bring a password of its own, or the service refuses it before anything is
  sent (`accountFor`, `internal/service/adguard.go`). Without that, a hijacked session could
  point the address at its own server and have the daemon send over the saved password. Keep it
  true of every integration, and of anything that later calls AdGuard Home on its own.
- **API tokens are read-only, and closed by default.** A token reaches only the GET routes in
  `tokenReadable` (`internal/api/tokens.go`): the status, the clients, and their traffic. Never
  add a client's config (it has the private key), its DNS log, the events, the stream, the
  settings, the integrations, the System routes (diagnostics, the backup, the snapshot list), or
  anything under `/api/auth`. A test names them and caps the
  list, so adding to it is a decision you have to make out loud, and a test checks that the
  OpenAPI document (`security`) says the same. A request that carries a token is a token's
  request whatever else it has: a cookie doesn't widen it. Making a token takes the password
  again, because it outlives a session. Its last use is written once an hour, not per request,
  and `TestWriteBudget` polls with one to keep it so.
- **A second factor is good once, and a right password forgives nothing.**
  `LoginWithCode` (`internal/service/auth.go`) and `confirmFactors` (`internal/service/totp.go`)
  clear the limiter's failures only after the password *and* the code have both passed. Clearing
  them after the password alone would let someone who has the password guess the six-digit code
  without limit (`TestWrongCodesAreLimitedWhateverThePassword`, `TestDisableTOTPLimitsWrongCodes`).
  A right password with no code is the login's first step, not a failure, and counts for nothing.
  `auth.VerifyTOTP` takes only a step later than `totp_last_step`, which `Store.UseTOTPStep`
  advances in one conditional `UPDATE`, so a code is never taken twice and two logins can't share
  one (`TestLoginRefusesACodeUsedAlready`, `TestUseTOTPStepIsAtomic`). Recovery codes are kept
  only as hashes and removed in the transaction that checks them. Turning 2FA on or off, and
  making new codes, take the password again (and a code, except the first), and end the other
  sessions. A wrong password or code from a logged-in session is a 400, never a 401, which the web
  app takes for a lapsed session. The secret, its `otpauth://` address, and the codes appear in
  one response each and nowhere else: not in a view, an event, or a log line
  (`TestEnrollAndEnableTOTP`).
- **Never retry an AdGuard Home 401 on a timer.** Five refusals block the daemon's address for
  15 minutes, and then the right password is refused too ("Verified facts"). A test remembers
  a refused account for 30 seconds, and the sync stops on a 401 until the connection changes, or
  a test or Sync now shows the account works (`refusedLogin`). Anything new that calls AdGuard
  Home on its own must honor `refusedLogin.has` first.
- **Name sync touches only what Drawbridge made.** An AdGuard Home client it has no record of is
  never edited or deleted, unless its name and addresses are exactly a client's (adopted without
  a write). It adds clients with the global settings on, and changes a client by reading it back
  and changing only its name and addresses. The tests for each of these are in
  `adguardsync_test.go`; a change to the sync that breaks one is breaking something real.
- **A backup holds the key, so it's always encrypted, and restore is never in the web UI.** The
  file carries `secret.key` beside the database it unlocks, so `backup.Write` refuses a
  passphrase under 12 characters, and nothing writes one unencrypted. Restore replaces the
  database, which includes the admin's password hash: only a root CLI command with the daemon
  stopped does it, because a web version would let a hijacked session take everything. Restore
  decrypts into a temporary file and checks it (the file is intact, the schema isn't newer, SQLite's
  integrity check, and the key opens the database) before it moves anything, keeps what it
  replaces with a time in the name, and undoes its moves on a failure. The tests for each of those
  are in `internal/backup/restore_test.go`. Each is a mutation check. The web download
  (`POST /api/system/backup`) takes the account's password again (`confirmPassword`, shared with
  making an API token, so a wrong one counts against the login limits), makes the whole file
  before it sends any of it, and writes each piece under its own deadline. Snapshots are listed
  in the web UI and never downloadable: they have no passphrase.
- **Migrations are append-only, and each ships with its fixture.** A shipped migration is never
  edited, renumbered, or removed: a database that applied it keeps the old text, and records only
  the highest version it applied, so numbers have no gaps. `TestReleasedMigrationsAreNeverChanged`
  pins each migration's statements (not its comments) by hash,
  `TestMigrationsAreNumberedWithoutGaps` catches two branches that each added `0012`, and
  `TestEveryMigrationHasAFixture` fails until the new migration has a feature in
  `internal/store/storetest`: seed rows for what it adds, written as raw SQL against that version's
  tables in the shape the release stored them (sealed secrets under their purposes, times in the
  format of the day), and the check that reads them back through the store. Then
  `TestUpgradeFromEverySchema` and `TestRestoreFromEverySchema` exercise it from every older schema
  for free, and fail on any row an upgrade loses or changes. A migration that changes old rows on
  purpose says so by changing the old fixture, not by loosening `storetest.Kept`. Add the new
  migration's hash to `released`, and the release's tag to `test/integration/upgrade-from.txt`.
- **A database from a newer build is read, never written.** `store.Open` leaves one alone and
  `Store.NewerSchema` says so. `serve` (`openService`) refuses it, exits 78 (`exitDatabaseNewer`),
  and `drawbridge.service` has `RestartPreventExitStatus=78`, so a downgrade doesn't make the daemon
  flap. `tunnel up` and `tunnel down` warn and go on, because they only read: keep it so (the
  reconciler's `State` interface is `Settings` and `Clients`, and a write there would be a decision
  to make out loud). Anything new that opens the database and writes must check `NewerSchema` first.
  `TestServeRefusesADatabaseFromANewerBuild` and `TestANewerDatabaseStopsTheDaemonNotTheTunnel`
  check both halves.
- **A migration needs its snapshot first.** `store.Open` with `WithMigrationSnapshots` (both
  units pass it) snapshots a database that has data before applying any migration to it, and
  returns an error without migrating when it can't. Don't make that a warning: a migration that
  goes wrong has nothing to go back to otherwise. The nightly snapshots are one DB-sized file
  written a day (`--snapshot-interval 0` turns them off), so they stay inside the SD card rule
  above, and `snapshot.Prune` touches only files whose names it made.
- **Secrets stay secret.** Private keys, PSKs, and the setup token are encrypted at rest (the
  `*_enc` columns) and never logged, except the setup token, which the journal shows until the
  admin account exists (§6.5). Session tokens and API tokens are stored only as SHA-256 hashes,
  and an API token is shown once, when it's made. Views never carry keys, and a 500 response
  never carries the internal error; the journal does (§7, §10).

## Verified facts, worth not re-deriving

All of these were established on 2026-09-26 unless noted.

**The reference platform's network stack.** NetworkManager manages the network there, and
systemd-networkd and ifupdown's `networking` are inactive. That's why its IPv6 kept working
without the `accept_ra=2` fix (NetworkManager sets `accept_ra` to 0 on its uplink, so the installer
leaves it alone; docs/PLAN.md §5.5), and why `wg0` must still be marked unmanaged in
NetworkManager. Don't generalize from it: on an ifupdown host, turning on IPv6 forwarding removes
the host's own SLAAC address unless `accept_ra` is 2 (docs/REQUIREMENTS.md). The integration tests
prove that in namespaces; a real ifupdown host hasn't run it.

**AdGuard Home's API** (2026-10-03: AdGuard Home v0.107.79 in a container, with real queries sent
to it; the OpenAPI document says none of the surprises below). `internal/adguard` is written to
this, its fake (`adguardtest`) answers this way, and `TestContract` runs the same checks against
the fake and, when `DRAWBRIDGE_ADGUARD_URL`, `_USER`, and `_PASSWORD` are set, a real one:

- Everything is under `/control`, with HTTP basic auth. `GET /control/status` checks an address
  and an account.
- Persistent clients: `GET /control/clients` (`clients` is `null` when there are none), and
  `POST /control/clients/add`, `/update` (`{name, data}`), and `/delete` (`{name}`). A client's
  `ids` accept IPs, CIDRs, MACs, or ClientIDs. AdGuard Home stores addresses in their short
  lowercase form, lowercases the rest, lists addresses first, and sorts the clients by name.
- **Every refusal is a 400 with a plain-text message**, never a 404 or a 409: a duplicate name
  or address (the message names the other client), an empty name, no `ids`, an invalid ID, and
  an update or delete of a client that isn't there. Names are case-sensitive (`min` and `MIN`
  coexist). A CIDR doesn't clash with an address inside it.
- **A client added with only a name and `ids` is stored with `use_global_settings` and
  `use_global_blocked_services` off, and filtering with them, so it isn't ad-blocked.** A query
  for a blocked domain from such a client's address was answered by the upstream resolver. With
  both flags `true` it was blocked, as for any other address. Name sync must send both.
- **An update replaces the whole client.** An update that sent only `name` and `ids` wiped the
  tags, upstreams, and per-client settings. Send back every field the listing returned.
- **Five failed logins block the caller's address for 15 minutes**, and then even the right
  password gets a bare 401 with no body, so a 401 can't tell a typo from a block. A request with
  no credentials counts as a failure. Never retry a 401 on a timer.
- `/control/querylog?search=` is a **substring** match on the client's address and on the
  domain: `10.8.0.1` also found `10.8.0.12` and `10.8.0.100`, and IPv6 addresses are logged in
  their canonical form (a search for an expanded or uppercase spelling found nothing). It pages
  with `older_than=<the page's oldest time>`, and takes a `limit` as big as 100000. A blocked
  entry has `reason: FilteredBlackList` and the rule in `rules[0].text`.
  `/control/querylog/config` has `enabled` and `anonymize_client_ip`, either of which would
  empty a client's view.
- To run one: `adguard/adguardhome` with a seeded `AdGuardHome.yaml` (an `http.address`, a user
  whose bcrypt hash `htpasswd -bnBC 10 "" password` makes, `dns.bind_hosts`, and a
  `user_rules` entry such as `||blocked.example^`) skips its install wizard. Sources other
  than 127.0.0.1 are easy in a sidecar sharing its network namespace
  (`docker run --network container:<name>`), which can bind any `127.x.y.z` address.
- DNS rewrites are under `/control/rewrite/*` (documented, not yet checked).

**The cloud container** (x86_64, kernel 6.18, Go 1.24.7 and Node 22 with npm 10.9.7
preinstalled, running as root):

- Kernel WireGuard is **not** available: `ip link add … type wireguard` fails with "Unknown
  device type."
- Network namespaces, TUN devices, and nftables 1.0.9 work.
- `nft` and `ip` aren't preinstalled. Install them with
  `apt-get update && apt-get install -y nftables iproute2`.
- The `go` command downloads Go 1.26.0 on its own, because that's `go.mod`'s minimum.
- It has **no IPv6** (`/proc/sys/net/ipv6` doesn't exist, and IPv6 addresses can't be
  added) and no `dummy` module. So the integration tests stop at their first IPv6 address
  here; they need CI.
- The M0 `.deb` installs, removes, and purges cleanly here. There's no running systemd, so
  `postinst` skips the service steps. `systemd-sysusers` created the `drawbridge` user, the
  binary served the UI as that user, and headless Chromium rendered the page with the live
  version.
- The M1 `.deb` installs too. `postinst` created `/etc/drawbridge/secret.key` (32 bytes,
  `root:drawbridge` 0640) and turned on IPv4 forwarding, and it warned and carried on when IPv6
  forwarding failed. Run as the `drawbridge` user with only `CAP_NET_ADMIN` (`setpriv
  --ambient-caps +net_admin`, as systemd will):
  - The daemon's effective capabilities were exactly `CAP_NET_ADMIN` (`CapEff` 0x1000).
  - Its control socket was `drawbridge:drawbridge` 0660, and the CLI worked through it as
    root, names with apostrophes included.
  - `tunnel up` failed with the "is the wireguard module loaded" hint, as it should without
    kernel WireGuard.

**Build tooling:**

- The latest Go is 1.27.1. `go.mod` says `go 1.26.0` with no `toolchain` line, because
  golangci-lint v2.14.0 (built with Go 1.26.8) refuses to run when the toolchain line targets a
  newer Go than it was built with.
- The sv CLI (0.17.1) puts SvelteKit's configuration inside `web/vite.config.ts`. There's no
  `svelte.config.js`.
- nfpm expands `${ARCH}` and `${VERSION}` in `arch` and `version`, but not in `contents` paths.
  It turns version `0.0.0-dev` into the Debian version `0.0.0~dev`.
- `systemd-analyze verify` (systemd 255) accepts both units. `systemd-analyze security
  --offline=true` rated the M0 unit 1.5 ("OK"); with `CAP_NET_ADMIN` (M1), both units rate 1.8
  ("OK").
- Go 1.27's gofmt aligns key-value tables differently from 1.26's (a map literal whose keys vary
  a lot in length), and CI's golangci-lint follows Go 1.27. Check with
  `"$(GOTOOLCHAIN=go1.27.1 go env GOROOT)/bin/gofmt" -l cmd internal test`, or write such tables
  as slices of structs, which have no alignment to disagree about.
- Go's `http.ServeMux` answers a path containing `..` with a 307 redirect to the cleaned path,
  and it refuses to register `GET /` alongside a method-less `/api/` pattern (it panics about
  the conflict).
- nftables 1.0.9 keeps a table's `comment` and reports it in `nft -j list table`. That's how
  the drift check reads the ruleset's revision. A transaction of `table …; delete table …;
  table … { … }` replaces the table atomically, even when it doesn't exist yet.
- The terminal QR code (rsc.io/qr, which always uses mask 0) decodes byte for byte back to the
  rendered config with zbarimg.
- `go test ./...` also finds a Go package inside `web/node_modules` (from `flatted`). The
  Makefile lists Drawbridge's own packages instead, and golangci-lint excludes `web/`.
- `nft_rt` and `nft_exthdr` (the MSS clamp's `rt mtu` and `tcp option maxseg`) are compiled
  into `nf_tables.ko`, as are meta, payload, and the set types (`nf_tables-objs` in the
  kernel's `net/netfilter/Makefile`). They never appear in `/sys/module` or to `modprobe`. The
  ruleset's only separate nftables modules are `nft_chain_nat` and `nft_masq`.
- GitHub's `ubuntu-24.04` runner (2026-09-26): kernel 6.17.0-1022-azure, `ip` and `nft`
  preinstalled, and root through passwordless sudo. The modules load without
  `linux-modules-extra`, the preflight's probes all pass, and the kernel tests pass there
  (about 40 seconds).
- On that runner (2026-10-04), the drawbridge processes the kernel tests start printed nothing to
  standard error, and the journal is reachable there (the journal tests don't skip). Setting
  `JOURNAL_STREAM` with a journal socket present reproduces the silence exactly, so the runner
  evidently hands `JOURNAL_STREAM` to every step: a binary that sees it logs to the journal
  (`newLoggerAt`). `TestMain` unsets it, and the journal tests set it on their own daemon. A test
  that reads a child's output needs that, or it passes in a container and fails in CI.
- Current GitHub Actions majors: `actions/checkout@v7`, `actions/setup-go@v7`,
  `actions/setup-node@v7`, `actions/upload-artifact@v7`, `actions/download-artifact@v8`, and
  `golangci/golangci-lint-action@v9`. All run on Node 24.

**The reference platform as a dev host** (a session can run there: a Raspberry Pi 5 on Debian
13, aarch64, a 6.18 Raspberry Pi kernel, passwordless sudo, real IPv6, kernel WireGuard):

- `make test-integration` passes there (about 40 seconds), and the M1–M3 `.deb` installs, runs,
  upgrades in place, and keeps the tunnel up.
- `make test-go` fails on every package with "ThreadSanitizer: unsupported VMA range": the
  kernel's 47-bit address space is one bit short of what the race detector supports. Run
  `go test ./cmd/... ./internal/...` without `-race` there; CI runs the race detector.
- `make test-e2e` can't start Playwright's Chromium: the host is headless and lacks its libraries
  (`libatk`, `libgbm`, and others). Run the tests in Playwright's container instead, which is
  rootless and leaves the host alone: `docker run --rm --ipc=host -v "$PWD":/work -w /work/web
  -e DRAWBRIDGE_BIN=/work/dist/drawbridge mcr.microsoft.com/playwright:v1.63.0-noble npx
  playwright test` (after `make deb` or `make build`; the image's version matches
  `@playwright/test`).
- Where Go and Node were installed by hand (`/usr/local/go/bin`, nvm's `bin`), the tools' PATH
  can miss them until the shell is restarted.

**The upgrade matrix** (2026-10-04, Docker Desktop's VM, a 7.0 aarch64 kernel with WireGuard):

- The repository's history starts at the "Initial commit" (`72174d0`, 2026-09-29), which is schema
  5. Schemas 1 to 4 can't be built again, which is why the data half of the matrix writes its old
  databases by hand. From schema 5 on, every schema's build is in the history and builds with a
  plain `go build` (`CGO_ENABLED=0`, no web build needed: the daemon starts without the UI).
- The oldest build's CLI has what the upgrade test uses: `tunnel up`, `serve` with
  `--drift-interval`, `server set --endpoint`, `client add|pause|config`, `admin create`, and
  `events`. A flag added since (`--safe-apply-window`) is passed only to this build.
- The units order the daemon after the tunnel unit (`After=`), so under systemd the two shouldn't
  open an old database together. Two processes that did, in a test, failed before the fix: one on
  a snapshot name that `snapshot.NewPath` had picked for both, the other on `table users already
  exists`, from the migration the first had applied.

**dpkg's maintainer script arguments** (2026-10-04, a Debian 13 container with a dummy package):
`postinst configure <old-version>` is the call for an upgrade, a downgrade, and a reinstall after a
plain `remove` (not a purge), all alike. Only a first install, or an install after `purge`, has an
empty `$2`. A script can't tell the first three apart by its arguments.

**The plan's example ruleset** (§5.3) passes `nft -c` and loads in a network namespace with
nftables 1.0.9.

**M2 in the cloud container** (no WireGuard needed), with the daemon in a network namespace whose
uplink (the interface with the default route) is on 192.0.2.0/24, and a second interface on
198.51.100.0/24:

- LAN detection found exactly 192.0.2.0/24.
- With only the app's allowlist, `/healthz` answered 200 from the LAN and 403 from the other
  network. With the rendered ruleset loaded as well, the other network's connection timed out
  (dropped) and the LAN still got 200.
- Headless Chromium loaded the real SvelteKit build under the CSP with no violations, and an
  injected inline script didn't run.
- The M2 `.deb`'s daemon, run as the `drawbridge` user with only `CAP_NET_ADMIN`, created its
  certificate in `/var/lib/drawbridge/tls` (key 0600, directory 0700), logged the fingerprint
  and the setup token, served HTTPS on `:51821` to loopback and to the container's LAN address,
  and `drawbridge admin setup-token` showed both.
- Go's `tls.LoadX509KeyPair` fills in `Certificate.Leaf` (Go 1.23 and later).

**Still unverified** (plan §16): routed IPv6 (M6) depends on the router accepting IPv6 static
routes and on the ISP's delegated prefix size, which vary by network.

**Verified on the reference platform** (docs/MANUAL_CHECKLIST.md): an IPv6 endpoint works when
the router allows inbound UDP 51820 to the host's stable address (with a real client,
2026-09-28).

**`drawbridge doctor`** (2026-09-29, the reference platform):

- Run as `drawbridge.service`'s sandbox (a transient `systemd-run` unit with the unit file's
  hardening properties, `--backend fake`, and a scratch state directory), every read worked:
  `/proc/sys`, `/sys/module`, `/run/systemd/timesync/synchronized`, netlink, `statfs`, the
  system resolver, and `nft -j list ruleset` with only `CAP_NET_ADMIN`. A binary under `/home`
  can't run in such a unit (`ProtectHome=yes`), so copy it somewhere under `/usr/local` first.
- The integration test (`test/integration/doctor_test.go`) drives the real kernel and real
  `nft -j` output: `accept_ra` 1 fails and 2 passes, forwarding off fails for each family, and
  another table's `policy drop` forward chain warns until a rule accepts `wg0`.
- `fwd` is an nftables keyword, so a chain can't be named that.

## Where things live

- `docs/PLAN.md` is the specification. Read it first. §13 has the planned repository layout.
- `docs/adr/` holds one decision record per decision in the plan's §3 (D1–D12).
- `docs/MANUAL_CHECKLIST.md` records what has actually run on real hardware.
- `docs/REQUIREMENTS.md` lists what a host and network need, and the known roadblocks.
- `docs/api-tokens.md` is the admin's guide to read-only API tokens and getting Homepage to use one.
- `docs/two-factor.md` is the admin's guide to two-factor authentication: turning it on, the
  recovery codes, getting back in without them, and what it does and doesn't cover.
- `docs/tls-certificate.md` is the admin's guide to serving their own TLS certificate: what it
  needs to cover, installing, renewing, and going back.
- `docs/backup-restore.md` is the admin's guide to backups: making one, keeping it, and restoring
  onto a fresh host.
- `cmd/drawbridge/` is the binary: `serve` (serve.go), `tunnel`, `server`, `client`,
  `events` and `admin` (admin_cmd.go), `doctor` (doctor_cmd.go), `version`, and `help`.
- The core engine, in `internal/`:
  - `model/` holds the domain types and validation, and `store/` is SQLite with embedded
    migrations.
  - `keys/` generates keys and seals secrets; `ipam/` allocates addresses.
  - `wg/` is the WireGuard backend, a kernel implementation plus a fake for tests.
    `firewall/` renders and applies the nftables table; its golden file is in `testdata/`.
  - `reconcile/` applies the database to the kernel.
  - `service/` is the management logic: clients and settings (service.go), setup, logins, and
    sessions (auth.go), and the event log (events.go). `control/` is the Unix-socket API and
    its client, and `views/` holds the JSON shapes both APIs share.
  - `clientconf/` renders client configs and terminal QR codes.
  - `diag/` holds the doctor's checks. The daemon runs them, not the CLI, because `nft -j list
    ruleset` needs `CAP_NET_ADMIN`. Every read goes through `diag.Host` (an `fs.FS` rooted at
    `/`, plus hooks for the routing table, the resolver, `statfs`, and nft), so a check is
    tested by handing it a `fstest.MapFS`. A check that can't read what it needs is a skip,
    never a fail. `service.Diagnose` feeds it the database's and the tunnel's state.
  - The event bus is in `service/eventbus.go`: `record` publishes each event it stores, and the
    stream subscribes. It never waits for a subscriber.
  - `journal/` logs to journald's native protocol, so a log record's attributes become fields
    (`DRAWBRIDGE_<KEY>`). `service.record` logs every event through it.
  - `adguard/` is the AdGuard Home REST client (persistent clients and the query log), and
    `adguard/adguardtest` an in-memory AdGuard Home that answers the way the real one was
    observed to ("Verified facts"). Tests use it, never a hand-written stub, so they fail the way
    the real one would.
  - `backup/` is the encrypted backup file (a chunked XChaCha20-Poly1305 stream under an Argon2id
    key, holding a gzipped tar of the manifest, the key, and the database) and `Restore`, which
    swaps it in. `store.Snapshot` is the `VACUUM INTO` copy it's made from.
  - `snapshot/` names, lists, and prunes the host's database snapshots, with no dependency on
    the store, which uses it for the one before a migration.
  - `store/storetest/` builds the databases older releases left (`Build`, with
    `store.WithSchemaLimit` making the schema) and reads them back through the current store
    (`Verify`); it has one feature per migration, and `Kept`, `Rows`, and `Schema` compare a
    database before and after an upgrade. Only tests import it.
  - `auth/` has password hashing, tokens, the login rate limiter, and TOTP codes and recovery
    codes (totp.go); `lan/` detects the LAN and builds the admin allowlist; `tlscert/` makes the
    self-signed certificate and holds the one in use (`Store`, which swaps it live and keeps the
    admin's own). `tlscert/tlscerttest` makes certificates for tests, so none is checked into the
    repository.
- `internal/api/` serves the JSON API (documented in `openapi.json`, with the security
  middleware in middleware.go) and the embedded web app. `internal/webui/` embeds the
  build that `make web` copies into `internal/webui/dist/`. `internal/sdnotify/` reports
  readiness to systemd, and `internal/version/` holds the build-time version.
- `test/packaging/test.sh` runs the maintainer scripts through real `dpkg`, with a fake
  `systemctl` that models what the scripts use (a unit is enabled when its link resolves, active
  by a marker, and a flag makes it fail to start), and records the calls.
- `test/integration/` holds the kernel tests (build tag `integration`), and the upgrade test with
  its list of older builds (`upgrade-from.txt`) and the script that builds and runs them
  (`upgrade.sh`).
- `web/` is the SvelteKit app. `src/routes/(auth)/` holds setup and login, and
  `src/routes/(app)/` everything behind a session (its `+layout.ts` is the guard). `src/lib/`
  has the API client (`api.ts`), formatting (`format.ts`), and components. `e2e/` holds the
  Playwright tests.
- `packaging/` holds:
  - Both systemd units.
  - The sysusers.d, sysctl.d, modules-load.d, and NetworkManager drop-ins.
  - `libexec/accept-ra`, which `postinst` runs (installed at `/usr/lib/drawbridge/accept-ra`).
  - The maintainer scripts and `nfpm.yaml`.
- `.github/workflows/ci.yml` is CI. Its golangci-lint version must match the Makefile's.
- `README.md` is the short project description.
