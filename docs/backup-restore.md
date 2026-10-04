# Backup and restore

A backup is one file that brings your Drawbridge back on a host that has nothing: the
clients (with their keys, so their configs keep working), the settings, the admin account, and
the event and traffic history.

It holds a snapshot of the database **and** the key the database's secrets are encrypted with
(`/etc/drawbridge/secret.key`), because the key is on the same SD card as the database, and a
backup without it would be useless after the card fails. So the file is encrypted with a
passphrase you choose, and **the passphrase is the only thing that protects it**. Without it, the
file can't be opened by you, by anyone, or by the project's developers.

## Make one

```bash
sudo drawbridge backup create
```

It asks for a passphrase twice, with nothing shown, and writes
`drawbridge-YYYYMMDD-HHMMSS.backup` in the current directory, readable only by you. Use
`--output FILE` to name it, and `--passphrase-file FILE` (a file only you can read) when it runs
from a script.

- The passphrase needs at least 12 characters. A stolen file can be guessed at offline, with no
  limit on the tries, so a long phrase is worth more than a clever short one.
- **Keep the file, and the passphrase, somewhere other than the host.** A backup on the SD card
  that fails isn't one. A password manager holds both well.
- Making one is in the event log (`Made a backup`), because the file holds every secret.
- Make one after you add clients. A client added later isn't in an older backup.

## Restore onto a fresh host

This is what the backup is for: the SD card failed, or you're moving to another Raspberry Pi.

1. Install Drawbridge on the new host, as you did the first time. Don't go through the web
   setup: the backup has your admin account.
2. Copy the backup to the host.
3. Stop the daemon. The tunnel unit doesn't need to stop.

   ```bash
   sudo systemctl stop drawbridge.service
   ```

4. Restore.

   ```bash
   sudo drawbridge backup restore drawbridge-20261003-223600.backup
   ```

   It asks for the passphrase, and checks the backup before it changes anything: that the file is
   whole, that the passphrase is right, that it isn't from a newer Drawbridge than this one,
   that SQLite finds the database healthy, and that the key opens it. If any of that fails, it
   says so and the host is as it was.

5. Start both units, so the tunnel comes up from the restored settings.

   ```bash
   sudo systemctl restart drawbridge-tunnel.service drawbridge.service
   ```

6. Log in with the account from the old host. Your clients' configs still work: the server's key
   is the same, so they reconnect without anything changing on the phones and laptops. The
   endpoint name in them has to point at this host now. Move the router's port forward (UDP
   51820), and your DNS record, if the address changed.

What a restore does:

- The database and the key are replaced with the backup's. Their owners and modes are set as the
  package sets them.
- **Every login is ended**, so everyone logs in again (the backup's logins are the old host's).
  API tokens are kept, so a dashboard that has one keeps working. If you restored because
  someone else had access, run `sudo drawbridge admin reset-password`, which revokes the
  tokens too.
- A backup from an older Drawbridge is brought up to date. One from a newer Drawbridge is
  refused: upgrade first.
- What it replaced is kept next to it: `drawbridge.db.before-restore-<time>` (with its `-wal` and
  `-shm` files, if it had them) and `secret.key.before-restore-<time>`. They're a way back if
  you restored the wrong file. **Delete them once you've checked that everything works**: the old
  key is in there.
- It adds a `Restored from a backup` event to the log.
- The web UI's TLS certificate isn't in a backup. The host keeps its own, so your browser's
  warning is about the host's certificate, as it was before. It names the VPN addresses as they
  were when the host made it; if you reach the UI through the VPN's address and the restored VPN has
  other addresses, stop the daemon, `sudo rm -r /var/lib/drawbridge/tls`, and start it for a new
  one.

It needs room for a second copy of the database beside the first while it works.

## When something goes wrong

| It says | Meaning |
|---|---|
| "wrong passphrase, or the backup is damaged" | The passphrase is wrong, or the file changed since it was made. If it's one you typed, try again; if the file was copied or downloaded, copy it again and compare checksums. Drawbridge can't tell the two apart. |
| "the backup is damaged or cut short" | The file opened, and then stopped making sense partway: it was edited or truncated. Use another copy. |
| "this isn't a Drawbridge backup" | It isn't a file `backup create` made, or it's a damaged one that's lost its start. |
| "this backup was made by a newer Drawbridge" | Upgrade this host's Drawbridge to the version that made it, or later, then restore. |
| "the Drawbridge daemon is running" | Stop it first: `sudo systemctl stop drawbridge.service`. |
| "the backup's key doesn't open its database" | The file's contents don't belong together. It can't have come from `backup create`. |

In every one of these, nothing was changed. If a restore fails while it's moving files (a full
disk, say), it puts back the ones it moved, and says so.

## Snapshots on the host

Besides a backup you make, Drawbridge keeps copies of its database on the host, in
`/var/lib/drawbridge/backups/`:

- **Nightly:** the daemon makes one when the newest is a day old, and keeps the newest seven.
  `--snapshot-interval` changes the interval (0 turns them off) and `--snapshot-keep` the number.
- **Before an upgrade changes the database:** `pre-migration-v<n>-<time>.db`, where `<n>` is the
  version the database was at, and the newest three are kept. If it can't be made (a full disk),
  the upgrade doesn't change the database, and says why.

They're a way back from a bad change or a bad upgrade, **not from a lost card**: they sit on the
same card as the database, with the key. Keep a backup somewhere else for that.

To go back to one, stop the daemon and restore it. A snapshot needs no passphrase, and the host's
own key stays, because it's what the snapshot was sealed with:

```bash
sudo systemctl stop drawbridge.service
sudo drawbridge backup restore /var/lib/drawbridge/backups/nightly-20261004-030000.db
sudo systemctl restart drawbridge-tunnel.service drawbridge.service
```

It's the same restore as above, with the same checks, and what it replaces is kept as
`*.before-restore-<time>`. The database is as it was when the snapshot was made, so changes since
then are gone: a client added after it isn't there. A snapshot from before an upgrade is brought up
to date when it goes in.

A snapshot that was made under another key (copied from another host, say) is refused, and says
that the host's key doesn't open it. Use a backup for that.

The snapshots take room: each is about the size of the database, so allow ten times its size
(`ls -lh /var/lib/drawbridge/drawbridge.db`) for the seven nightly ones and the three from
upgrades. `sudo drawbridge doctor` checks the free space.

## Not in a backup

- The web UI's certificate (above), and the log in the journal (the event log is in the backup).
- Anything outside Drawbridge: the router's port forward, your DNS records, and AdGuard Home
  itself. Drawbridge's connection to AdGuard Home (the address, the account, and the password)
  is in it, and the client names it added there come back with the next sync.

A download from the web UI isn't built yet.
