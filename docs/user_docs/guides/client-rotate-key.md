---
title: Rotate Client Keys
---

Rotating a client's keys gives it a new key pair and a new preshared key. The old config stops
working **at once**: the device is out of the tunnel until you import the new one. Do this when a
device is lost or its config may have leaked, and you want to keep the client's name and
addresses.

If you only want a device cut off,
[pause or delete](client-configuration.md#pause-rename-and-delete) the client instead.

## Rotate the keys

1. Open the client's page and press **Rotate Keys**, then confirm.
2. The new config's QR code appears. Scan it with the WireGuard app, or press **Download .conf**
   and import the file.

On the host:

```bash
sudo drawbridge client rotate-keys phone
```

It asks before it changes anything; `--yes` skips the question. Then show the new config with
`drawbridge client qr phone`.

## What to expect

- The device can't connect until you import the new config.
- The client shows as **outdated** until you have shown or downloaded the new config.
- The log records that the keys were rotated.

This changes one client. To change the key every client uses to reach the server, see
[Rotate the server key](server-rotate-key.md).
