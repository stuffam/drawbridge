---
title: Logs
---

Drawbridge keeps a log of what happens: changes, logins, clients connecting and disconnecting, and
problems it corrected. Each entry says what happened, when, who did it (you in the web UI, the
command line, or Drawbridge itself), which client it was about, and the details. It never records
a password, a private key, or a code.

## On the Logs page

Open **Logs** in the header. New entries appear as they happen, and the filters narrow the list:

| Filter | Choices |
|---|---|
| **Show** | Everything, Changes and logins, Client connections, or Drawbridge itself. |
| **Event** | One kind of entry, such as Connected, Roamed, Logged in, or Failed login. The list depends on **Show**. |
| **Client** | One client. |
| **When** | Any Time, Last Hour, Last 24 Hours, Last 7 Days, or Last 30 Days. |

**Load Older** shows earlier entries, and **Export CSV** saves the entries that match the filters
as a file. A client that **Roamed** is one whose address changed while it was connected, such as a
phone moving from Wi-Fi to mobile data.

A client's own page has an **Activity** list with just its entries.

## On the command line

```bash
sudo drawbridge events
```

It shows the newest entries first, 50 by default. `--limit N` shows up to 500, and `--client NAME`
shows one client's.

## In the system journal

Under systemd, every entry is also written to the system journal, with its parts as fields you can
filter on. This shows what happened to one client:

```bash
sudo journalctl -u drawbridge DRAWBRIDGE_CLIENT=phone
```

`DRAWBRIDGE_EVENT` filters by the kind of entry. The journal also has the daemon's own messages
and errors, and `sudo journalctl -u drawbridge -u drawbridge-tunnel` shows both services.
