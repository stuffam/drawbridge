---
title: TLS Certificate
---

The web UI is served over HTTPS. On first start the daemon makes a **self-signed** certificate
for the host's names and addresses, so the connection is encrypted from the start, but a browser
can't tell it from a forgery and warns until you trust it. The setup token's command
(`sudo drawbridge admin setup-token`) prints the certificate's SHA-256 fingerprint so you can check
that the warning is about this certificate.

If you'd rather not click through a warning, give Drawbridge a certificate your devices already
trust. This guide is for that.

## What you need

- A certificate and its private key, as PEM text. The certificate file should be the **full
  chain**: the server's own certificate first, then the intermediates (Let's Encrypt's
  `fullchain.pem` is that). The key must **not** be protected by a passphrase.
- A certificate that names what you type in the browser. The UI is reachable only from your home
  network and the VPN, never the internet, so a certificate authority can't reach it to check you
  control it. The ways to get a trusted one anyway:
    - **A public name with a DNS challenge.** Choose a name in a domain you own, such as
      `vpn.example.com`, point it (in your home DNS, or the VPN's) at the host, and get a
      certificate for it from an ACME client that uses the DNS-01 challenge (Let's Encrypt supports
      it, and most DNS providers have a plugin). Then open the UI at
      `https://vpn.example.com:51821`.
    - **A private certificate authority** your devices already trust (a company's, or one you run
      and installed on your devices).

The names the certificate covers matter: a browser warns if the address in its bar isn't one of
them. Drawbridge tells you which names a certificate covers and warns if it covers none of the
host's own (its hostname, its `.local` name, its addresses) or the endpoint you set.

## Install it

From the host:

```bash
sudo drawbridge tls install --cert fullchain.pem --key privkey.pem
```

Or open **System** in the web UI, find **Web UI Certificate**, pick or paste the two files, enter
your password again, and press **Install Certificate**.

Both are checked before anything changes. A certificate that has expired, names nothing, isn't
for servers, uses a weak RSA key (under 2048 bits), or doesn't match the key is refused, with the
reason. Once installed, **the next connection uses it**: nothing restarts. The page you installed
it from keeps the old certificate until you reload it.

`sudo drawbridge tls show` prints what's in use: where it came from, its names, issuer, dates, and
fingerprint.

## Renewing it

**Nothing renews an installed certificate**, and Drawbridge won't swap in the self-signed one on
its own when yours runs out. The System page's checks (and `sudo drawbridge doctor`) warn 30 days
before it expires and fail when it has.

Most ACME clients can run a command after each renewal. With certbot, a deploy hook installs the
renewed files:

```bash
sudo certbot renew --deploy-hook 'drawbridge tls install \
  --cert "$RENEWED_LINEAGE/fullchain.pem" --key "$RENEWED_LINEAGE/privkey.pem"'
```

(certbot sets `RENEWED_LINEAGE` to the certificate's directory and runs hooks as root. Other
clients have their own way to run a command after renewing.) Run `sudo drawbridge tls show`
afterward to see the new expiry.

## Going back

```bash
sudo drawbridge tls reset
```

or **Use the Self-Signed Certificate** on the System page. The installed certificate and its key
are forgotten, and the self-signed one serves again (a new one is made if it had run out).

## Where it's kept

The chain and the key are saved in one file, `/var/lib/drawbridge/tls/uploaded.pem`, readable only
by the `drawbridge` user. The key isn't encrypted there, like the self-signed key beside it,
because the web server needs it at startup. It is never shown in the UI, the event log, or the
journal.

**It isn't in a backup** ([docs/backup-restore.md](backup-restore.md)). Keep your own copy, and
install it again after restoring onto a new host.

If the file can't be read when the daemon starts (it was damaged, say), the daemon logs why and
serves the self-signed certificate meanwhile, so the web UI stays reachable. `sudo drawbridge tls
reset` clears the file; then install again.

## From the API

`GET`, `PUT`, and `DELETE /api/system/certificate`, documented in the OpenAPI document
(`internal/api/openapi.json`). They need a login, and an API token can't use them.
