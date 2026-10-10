---
title: API Tokens
---

A dashboard such as [Homepage](homepage.md) can't log in to Drawbridge: it can send a header, but
it can't fill in a form, keep a cookie, or send the `X-Drawbridge` header. An API token is a
credential for that. It's read-only, it shows a few numbers, and you can revoke it.

## What a token can read

A token works on these `GET` routes and no others:

| Route | What it returns |
|---|---|
| `/api/server/status` | whether the tunnel is up, how many clients there are, and how many are online (a handshake in the last three minutes), paused, and outdated, and the bytes received and sent since the tunnel started |
| `/api/clients` and `/api/clients/{id}` | the clients, with their addresses, public keys, and when each last connected |
| `/api/clients/{id}/traffic` and `/sessions` | one client's traffic history and connections |
| `/api/traffic` and `/api/traffic/clients` | traffic history, in total and per client, as a list of samples |
| `/api/traffic/total` | every client's traffic over a range, added up into one received and one sent number |

Everything else answers `403`, with no exceptions for a token that looks right. In particular a
token **can't** read a client's config (it holds the client's private key) or its DNS queries, the
event log, or the settings. It can't change anything, and it can't make, list, or revoke tokens.
Those routes need a login.

The client names, addresses, and endpoints in `/api/clients` are still information about your
household. Treat a token like a password you've written in another program's config file.

## Make one

1. Open the Account page (the person icon in the header, then Settings) and find **API Tokens**.
2. Give the token a name, such as `Homepage`, so you know which to revoke later.
3. Type your password again. A token outlives the session that made it, and a password change,
   so a logged-in browser alone can't make one.
4. Press **Make Token**, and **copy the token now**. It looks like `dbt_` and 43 more characters.
   Drawbridge keeps only a hash of it, so it can't be shown again. If you lose it, revoke it and
   make another.

You can have 20 tokens, and each needs a different name.

If you've turned on [two-factor authentication](two-factor.md), a token still works without a
code: a dashboard can't type one. That's why a token is read-only. Making a token asks for your
password again, as it always did, but not for a code.

## Try it

```bash
curl -k -H 'Authorization: Bearer dbt_…' https://your-host:51821/api/server/status
```

```json
{"tunnel_up":true,"clients":4,"paused":1,"online":2,"outdated":0,"receive_bytes":734003200,"send_bytes":125829120}
```

`-k` tells `curl` to skip the certificate check, which is fine for a quick look: Drawbridge's own
certificate is self-signed. A program that keeps using the token should check the certificate
instead; [the Homepage page](homepage.md#when-homepage-cant-reach-it) shows how.

## Revoke, and replace

On the Account page, **Revoke** ends a token at once, and anything using it gets `401`. To replace
one: make the new token, put it in the dashboard, then revoke the old one.

Each token shows when it was last used, to the hour. A token that was never used, or hasn't been
for months, is one to revoke.

- Changing your password doesn't revoke tokens, so a routine change doesn't break your
  dashboards. If you changed it because someone else had access, revoke the tokens too.
- Resetting the password from the command line (`drawbridge admin reset-password`) revokes every
  token, along with every session. That's the "I've lost control, take it back" path.

The event log records a token being made, revoked, or refused a password, never the token.
