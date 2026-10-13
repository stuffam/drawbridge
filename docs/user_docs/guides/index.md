---
title: Guides
---

Each guide covers one topic. If you haven't installed Drawbridge yet, start with
[Getting Started](../getting-started.md), and if something isn't working, with
[Troubleshooting](troubleshooting.md). The **Help** icon in the web UI's header opens this page.

## Clients

- [Client Configuration](client-configuration.md): add a client, connect a device to it, and pause,
  rename, or delete it.
- [Rotate Client Keys](rotate-client-keys.md): give a client new keys, which cuts off its old config
  at once.

## Server

- [Server Configuration](server-configuration.md): the endpoint, the tunnel, and the changes that
  could lock you out.
- [Web UI Access](web-ui-access.md): who can reach the web UI, and how to let in another network.
- [Rotate the Server Key](rotate-server-key.md): give the server a new key pair, which stops every
  client until it has its new config.
- [DNS Settings](dns-settings.md): choose the DNS servers your clients use, or use a resolver on
  the host.
- [Addressing](addressing.md): coming soon.
- [AdGuard Home](adguard-home.md): connect it, keep its client names in sync, and see a client's
  recent DNS queries.
- [Pi-hole](pi-hole.md): coming soon.

## System

- [Diagnostic Tools](diagnostics.md): the checks of the host and its network, on the System page,
  the dashboard, and the command line.
- [TLS Certificate](tls-certificate.md): serve your own certificate in place of the self-signed one.
- [Backup & Restore](backup-restore.md): make an encrypted backup, and restore it onto a fresh host.
- [Database Snapshots](snapshots.md): the copies of its database that the host keeps, and how to go
  back to one.
- [Logs](logs.md): what Drawbridge records, and where to read it.
- [Upgrade](upgrade.md): install a newer package over the old one.
- [Remove](remove.md): remove the package, and keep or delete your data.
- [Troubleshooting](troubleshooting.md): what to try when a client can't connect, the web UI won't
  load, or you can't sign in.

## Other Settings and Tools

- [API Tokens](api-tokens.md): read-only tokens for a dashboard that can't log in.
- [Homepage Dashboard](homepage.md): show Drawbridge's status in Homepage.
- [Two-Factor Authentication](two-factor.md): ask for a code from your phone at login.
- [CLI Tools](cli-tools.md): manage Drawbridge from a terminal on the host.
