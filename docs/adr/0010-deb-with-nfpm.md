# ADR 0010: Distribute as a .deb built with nfpm

- **Status:** Accepted (2026-09-26), amended 2026-10-04
- **Plan reference:** docs/PLAN.md §3, D10

## Context

Drawbridge should install natively, with no Docker, and upgrade through the normal Debian tools.

## Decision

Build `.deb` packages for arm64 (such as a Raspberry Pi) and amd64 (PCs and virtual machines) with
nfpm, from
`packaging/nfpm.yaml`. The maintainer scripts create the system user with `systemd-sysusers`
(falling back to `adduser`). When systemd is running, a first install enables both units, and an
upgrade restarts the daemon and keeps an admin's `systemctl disable` (docs/PLAN.md §11). Removal
stops the units and leaves them enabled; purge deletes `/var/lib/drawbridge`, `/etc/drawbridge`, and
the units' links but keeps the system user, as Debian policy asks. A `vX.Y.Z` tag on `main` starts
`release.yml`, which runs CI's jobs on the tag, builds both packages, and drafts a GitHub Release
holding them, their checksums, and `install.sh`. The maintainer installs the draft's own files on
real hardware and then publishes it (docs/PLAN.md §11.1). Tags and published files are never
changed: a bad release is fixed by the next version.

## Consequences

- The Debian version is the bare version, so `0.0.0-dev` becomes `0.0.0~dev` and sorts before
  `0.0.0`.
- M0 already ships a minimal package, so the hello-world build installs on a real host the same way
  releases will.
- nfpm doesn't expand variables in `contents` paths, so `make package` stages each architecture's
  binary at `dist/package/drawbridge`.
- CI uploads the packages as the `drawbridge-deb` artifact on every run.
- A release is a rebuild from the tag, not CI's artifact, because the version is in the binary. CI
  runs on the tag first, so the rebuild is tested.
- GitHub's "latest" release skips pre-releases, which `install.sh` reads, so releases before v1.0
  are ordinary releases.
