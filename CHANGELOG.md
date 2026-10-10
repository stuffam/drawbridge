# Changelog

All notable changes to Drawbridge are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

A release's notes on GitHub are its section here, so each section is written for the admin who is
about to install or upgrade: what they need to know first, then what changed. The heading is the
version with its `v`, in brackets, and the release's date, as in `## [v1.0.0] — 2026-10-05`, and a
version without a section can't be released. A release is made from a pull request that turns the
`[Unreleased]` section into the new version's, puts a fresh empty `[Unreleased]` above it, and
updates the links at the foot of the file (docs/PLAN.md §11.1). Those links, and the `---` lines
between sections, aren't part of a release's notes.

---

## [Unreleased]

---

## [v0.1.0-rc.2] — 2026-10-07

The first release candidate of Drawbridge, a self-hosted web manager for a WireGuard VPN server.
It's a pre-release: the same software as v0.1.0 unless something turns up, published first so the
release's files, their names, and `install.sh` can be tried the way you'd use them before v0.1.0
goes out. GitHub never calls a pre-release "latest", so `install.sh` installs this one only with
`--version v0.1.0-rc.2`.

It's the second candidate. `v0.1.0-rc.1` was tagged first and never published: GitHub rewrites a
`~` in a release's file names, so the checksums that release listed matched no file. The files are
now named for the version (`drawbridge_0.1.0-rc.2_arm64.deb`), and the package's own version is
`0.1.0~rc.2`.

### Notes

- **There's nothing to upgrade from.** This is the first release. Install it on a Debian-family host
  with systemd, arm64 or amd64, by the [install
  guide](https://github.com/stuffam/drawbridge/blob/v0.1.0-rc.2/docs/install.md), after reading
  what the host and your network need
  ([requirements](https://github.com/stuffam/drawbridge/blob/v0.1.0-rc.2/docs/REQUIREMENTS.md)).
- **Where it has run.** A Raspberry Pi 5 on Debian 13, with NetworkManager, real phones, and a
  laptop. CI runs the kernel WireGuard tests and the upgrade tests on Ubuntu 24.04 (amd64). The
  amd64 package hasn't been installed on a real machine yet.
- **A downgrade isn't supported, so make a
  [backup](https://github.com/stuffam/drawbridge/blob/v0.1.0-rc.2/docs/backup-restore.md) before
  you upgrade.** Once a version has changed the database's layout, an older Drawbridge reads it and
  never writes it: its web UI won't start, and the VPN keeps running.
- **Some things haven't run on real hardware yet:** changing the listen port or rotating the
  server's key from a phone behind a router's NAT, two-factor authentication with a real
  authenticator app, a restore onto a freshly flashed card, a certificate from a public CA, and a
  week or more of traffic history behind the long chart ranges. The [manual
  checklist](https://github.com/stuffam/drawbridge/blob/v0.1.0-rc.2/docs/MANUAL_CHECKLIST.md) lists
  each one and what was seen so far.
- **One admin, and the same settings for every client.** There's no per-client DNS, MTU, access
  rule, or expiry yet, and a client has a name and nothing else.
- **No router setup or troubleshooting guide yet.** The
  [requirements](https://github.com/stuffam/drawbridge/blob/v0.1.0-rc.2/docs/REQUIREMENTS.md) list
  the known roadblocks.
- **`drawbridge doctor` doesn't compare the endpoint's A record with your public IPv4 address.** It
  warns when the name resolves to an address that can't be reached from the internet, but telling a
  stale record from a current one would mean asking a third party what your address is.

### Added

- **The VPN.** Add, remove, pause, resume, and rename clients, and hand each one out as a QR code or
  a `.conf` file. IPv4 and IPv6, with NAT44 and NAT66, and an IPv6 endpoint when your router allows
  it. Drawbridge manages only its own nftables table, `inet drawbridge`. The tunnel is a separate
  service from the web UI, so the VPN keeps running if the web UI stops, restarts, or is upgraded.
- **The web UI, the command line, and the API.** A dashboard, client pages, charts, a log with
  filters and CSV export, and settings for the endpoint, addresses, MTU, keepalive, and DNS.
  Everything is also in `drawbridge` (`client`, `server`, `events`, `doctor`, `backup`, `tls`,
  `admin`), and in a JSON API with an OpenAPI document. A read-only API token lets a dashboard such
  as Homepage show the status without logging in.
- **Monitoring.** Connect, disconnect, and roam events, session history, and traffic charts from one
  minute to 90 days, and pages that update as things happen. Writes to the database are batched, so
  an SD card isn't worn out.
- **AdGuard Home, if you use it.** Client names in its query log, and what each client looked up on
  the client's page.
- **Safe by default.** The web UI answers only your home network and the VPN, enforced in the app
  and in the firewall. Passwords are Argon2id with rate limiting, and two-factor authentication (an
  authenticator app, with recovery codes) is optional. You can serve your own TLS certificate. The
  daemons run as an unprivileged user with only `CAP_NET_ADMIN`, and secrets are encrypted at rest.
- **Operations.** `drawbridge doctor` runs 13 checks of the host and network with a fix for each
  (also on the System page, and as a dashboard banner). A settings change that could cut you off,
  such as the listen port, is undone after 60 seconds unless you keep it. A client whose config is
  out of date is flagged, and client and server keys can be rotated. Encrypted backups restore onto
  a fresh host, and the host keeps nightly snapshots and one before each migration.
- **Releases.** The packages and install script come with checksums, SBOMs, and build provenance,
  and the same commit builds the same package byte for byte.

---

[Unreleased]: https://github.com/stuffam/drawbridge/compare/v0.1.0-rc.2...main
[v0.1.0-rc.2]: https://github.com/stuffam/drawbridge/releases/tag/v0.1.0-rc.2
