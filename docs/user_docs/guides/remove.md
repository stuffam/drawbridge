---
title: Remove
---

Drawbridge is removed using the following command:

```bash
sudo apt remove drawbridge
```

stops both services, and deletes the `wg0` interface and the `inet drawbridge` firewall table. It
keeps your data, so installing the package again brings back the same clients, settings, and
certificate.

```bash
sudo apt purge drawbridge
```

also deletes `/var/lib/drawbridge` and `/etc/drawbridge`, the database, the certificate, and the
key included. **This can't be undone,** and a backup is the only way back, so make one first. The
`drawbridge` user stays, as Debian packages leave their users, so a later install owns its files
the same way.
