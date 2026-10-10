---
title: Remove
---

To take Drawbridge off the host, remove or purge its package.

## Remove it, and keep your data

```bash
sudo apt remove drawbridge
```

This stops both services, and deletes the `wg0` interface and the `inet drawbridge` firewall
table. It keeps your data, so installing the package again brings back the same clients, settings,
and certificate. IP forwarding, which the package turned on, stays on until the next reboot.

## Purge it, and delete your data

```bash
sudo apt purge drawbridge
```

This does the same, and also deletes `/var/lib/drawbridge` and `/etc/drawbridge`: the database, the
certificate, and the key included. **This can't be undone,** and a [backup](backup-restore.md) is
the only way back, so make one first. The `drawbridge` user stays, as Debian packages leave their
users, so a later install owns its files the same way.
