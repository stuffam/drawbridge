---
title: Homepage Dashboard
---

[Homepage](https://gethomepage.dev) is a dashboard that can show Drawbridge's status, such as how
many clients are online. It can't log in to Drawbridge, so it uses a
[read-only API token](api-tokens.md). Make a token first, then set up the widget below.

## The widget

In Homepage's `services.yaml`, under the service that should show it:

```yaml
widget:
  type: customapi
  url: https://your-host:51821/api/server/status
  headers:
    Authorization: Bearer dbt_…
  mappings:
    - field: online
      label: Online
    - field: clients
      label: Clients
    - field: paused
      label: Paused
```

The Account page shows this with the real address and the new token filled in, to copy.

## Traffic totals

Homepage shows a value; it can't add up a list. So there are two routes that return the sums.

**Since the tunnel started.** `/api/server/status` has `receive_bytes` and `send_bytes`, the
counters of the clients that are in the tunnel now, added up:

```yaml
    - field: receive_bytes
      label: Received
      format: bytes
    - field: send_bytes
      label: Sent
      format: bytes
```

They're counted at the server, so "received" is what the clients sent up, and "sent" is what they
downloaded. A client's counters run from when it joined the tunnel, so the sums **fall when a client
is paused or deleted** (a paused client has left the tunnel), a resumed client starts at zero, and
they all start over when the tunnel restarts. They're a "since the tunnel last started" number, not
a total that only grows.

**Over a range.** `/api/traffic/total?range=24h` adds up the stored history. The range is `1m`,
`1h`, `12h`, `24h` (the default), `7d`, `30d`, or `90d`:

```yaml
widget:
  type: customapi
  url: https://your-host:51821/api/traffic/total?range=24h
  headers:
    Authorization: Bearer dbt_…
  mappings:
    - field: receive_bytes
      label: Received (24 h)
      format: bytes
    - field: send_bytes
      label: Sent (24 h)
      format: bytes
```

The reply is one object:

```json
{"range":"24h","since":"2026-10-03T21:15:00Z","until":"2026-10-04T21:15:00Z","receive_bytes":734003200,"send_bytes":125829120}
```

- A restart or a paused client doesn't take anything out of it. **Deleting a client does**: its
  history goes with it, so the total falls by its share.
- It's the same history the charts draw, so it stops at the last complete bucket. That's a minute
  behind for the ranges up to 24 h, and up to an hour behind for 7d, 30d, and 90d.
- History is kept for 90 days, so there's no all-time total. For a longer one, ask for `90d` and
  keep the number somewhere else.

For more than one range, use a widget for each. The reply is one object, not a list, so
`dynamic-list` doesn't apply to it.

## When Homepage can't reach it

Three things can stop it, and each gives a different message in Homepage's log.

**`403` and "only reachable from the home network and the VPN".** Drawbridge only answers
requests from the home network, the VPN, and any extra sources you've added, tokens included. A
Homepage in Docker on the same machine connects from Docker's own network, which isn't one of
them. Add it (use the subnet your Docker network has: `docker network inspect bridge`):

```bash
sudo drawbridge server set --admin-allow 172.17.0.0/16
```

This replaces the list of extra sources, so if you've added others, such as a Tailscale range,
name them all, separated by commas.

**`DEPTH_ZERO_SELF_SIGNED_CERT`, or "self signed certificate".** Drawbridge makes its own
certificate, and Homepage, which runs on Node, doesn't trust it. Copy the certificate to where
Homepage can read it, and tell Node to trust it:

```bash
sudo cp /var/lib/drawbridge/tls/cert.pem /path/to/homepage/config/drawbridge.pem
```

and in Homepage's environment: `NODE_EXTRA_CA_CERTS=/app/config/drawbridge.pem` (the path inside
its container). This trusts that one certificate, and checks it. Drawbridge replaces the
certificate about 30 days before it expires, which is about every two years; copy it again then.
If you [installed your own certificate](tls-certificate.md) that Node already trusts, skip the
copying: the `url` only has to use a name that the certificate covers.

**`ERR_TLS_CERT_ALTNAME_INVALID`.** Node trusts the certificate, but the name in the `url` isn't
one the certificate was made for. It covers the host's name, that name with `.local`,
`localhost`, and the host's addresses when it was made:

```bash
openssl x509 -in /var/lib/drawbridge/tls/cert.pem -noout -ext subjectAltName
```

Use one of those in the `url`. A public name that your router resolves to the host, such as
`vpn.example.com`, isn't on the list.

As a last resort, `NODE_TLS_REJECT_UNAUTHORIZED=0` makes Node skip the check. It does so for
every request that Homepage makes, not just this one, so prefer the two steps above.

| What Homepage logs | Meaning |
|---|---|
| `401` "invalid API token" | The token is wrong, was revoked, or isn't there. Check for a stray space or line break in the file. |
| `403` "an API token can't use this endpoint" | The `url` is a route a token can't read. |
| `403` "only reachable from the home network and the VPN" | The address Homepage connects from isn't allowed (above). |
