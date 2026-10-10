---
title: Database Snapshots
---

Besides a [backup](backup-restore.md) you make, Drawbridge keeps copies of its database on the
host, in `/var/lib/drawbridge/backups/`:

- **Nightly:** the daemon makes one when the newest is a day old, and keeps the newest seven.
- **Before an upgrade changes the database:** `pre-migration-v<n>-<time>.db`, where `<n>` is the
  version the database was at, and the newest three are kept. If it can't be made (a full disk),
  the upgrade doesn't change the database, and says why.

They're a way back from a bad change or a bad upgrade, **not from a lost disk**: they sit on the
same disk as the database, with the key. Keep a backup somewhere else for that.

The System page lists the snapshots, with the time and size of each, and doesn't offer them for
download: a snapshot has no passphrase, and the database has your password hash in it. A backup is
how a copy leaves the host.

## Go back to one

Stop the daemon and restore it. A snapshot needs no passphrase, and the host's own key stays,
because it's what the snapshot was sealed with:

```bash
sudo systemctl stop drawbridge.service
sudo drawbridge backup restore /var/lib/drawbridge/backups/nightly-20261004-030000.db
sudo systemctl restart drawbridge-tunnel.service drawbridge.service
```

It's the same restore as for a backup, with the same checks, and what it replaces is kept as
`*.before-restore-<time>`. The database is as it was when the snapshot was made, so changes since
then are gone: a client added after it isn't there. A snapshot from before an upgrade is brought up
to date when it goes in.

A snapshot that was made under another key (copied from another host, say) is refused, and says
that the host's key doesn't open it. Use a backup for that.

## Disk space

The snapshots take room: each is about the size of the database, so allow ten times its size
(`ls -lh /var/lib/drawbridge/drawbridge.db`) for the seven nightly ones and the three from
upgrades. `sudo drawbridge doctor` checks the free space.
