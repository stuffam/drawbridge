---
title: Upgrade
---

Install the newer package using the following command:

```bash
sudo apt install ./drawbridge_<new-version>_<arch>.deb
```

The web UI restarts, and the VPN stays up: the tunnel service isn't restarted, and a client that's
connected stays connected. When the new version changes the database's layout, it saves a snapshot
beside the database first. A service you turned off with `systemctl disable` stays off.

Make a [backup](backup-restore.md) first if you want a copy that's safe off the host.

**Downgrading isn't supported.** An older Drawbridge reads a database that a newer one has changed
and never writes it, so if you install an older package anyway, the web UI won't start (the
journal says why) and the VPN keeps running. Install the newer version again, or
[restore a backup](backup-restore.md) made by the older one.
