---
title: Rotate the Server Key
---

The server's key is how every client recognizes your server. Rotating it gives the server a new
key pair. Every client's config holds the old public key, so **every client stops working at
once**, and a device can't connect again until you import its new config. Do this only if the
server's key may have leaked, not to tidy up.

## Before you start

- **Do it from your home network, not through the VPN.** You would be cut off, and the change is
  undone after a minute unless you keep it. See below.
- **Have each device to hand.** You will give every client its config again.

## Rotate the key

1. Open **Settings**, find **Server Key**, and press **Rotate the Key…**, then confirm.
2. The change applies at once, and a bar at the top of the page counts down a minute. Press **Keep
   Changes** to keep the new key, or **Undo Now** to go back. If you do nothing, or the host
   restarts first, the change is undone.
3. For each client, open its page, show the QR code or download the config, and import it on the
   device.

On the host:

```bash
sudo drawbridge server rotate-key --safe
sudo drawbridge server confirm
```

It asks before it changes anything; `--yes` skips the question. Without `--safe` the command keeps
the new key at once. Run it from the host's own terminal, not over the VPN.

## After the rotation

- Every client that had been handed a config shows as **outdated** until you hand it the new one.
- The log records that the server's key was rotated, with the new public key and never the
  private one.

To change one client's keys instead, see [Rotate client keys](client-rotate-key.md).
