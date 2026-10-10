---
title: AdGuard Home
---

If your DNS resolver is [AdGuard Home](https://github.com/AdguardTeam/AdGuardHome), Drawbridge can
talk to it. This is optional, and everything else works without it. It adds two things:

- **Names.** AdGuard Home's query log and statistics say "phone" instead of `10.8.0.23`.
- **Queries.** A client's page shows its recent DNS queries.

Connecting to AdGuard Home doesn't make your clients use it. To do that, choose **This server**
in [DNS settings](dns-settings.md).

## Connect it

You need AdGuard Home running and reachable from the host, and an account on it. An account made
for Drawbridge is best (`AdGuardHome.yaml` can list several users).

1. Open **Settings** and find the **AdGuard Home** card.
2. **Address** is where AdGuard Home's web interface is, such as `http://127.0.0.1:3000`.
   Drawbridge adds `/control` itself.
3. Enter the **Username** (leave it empty if AdGuard Home has no login) and the **Password**. The
   password is stored encrypted and never shown again. Leave the field empty later to keep the
   saved one, and enter it again if you change the address or the username.
4. Press **Test Connection**. It reports AdGuard Home's version and whether its DNS server is
   running, protection is on, and its query log is on. It warns about anything that works against
   what Drawbridge uses it for, and also says whether AdGuard Home answers on the server's VPN
   addresses.
5. Turn on **Use AdGuard Home**, and **Name clients in AdGuard Home** if you want the names. Then
   press **Save Connection**.

## What name sync does

Drawbridge adds each client to AdGuard Home under its name and its VPN addresses. It keeps them up
to date as you add, rename, and delete clients, and looks again every few minutes. The card says
when it last synced and how many clients have their name there.

- It changes only the clients it added, and keeps whatever else you set on them in AdGuard Home.
  A client it didn't add is left alone, unless its name and addresses are exactly a client's, in
  which case Drawbridge takes it over without changing it.
- If a client can't be named, such as when its name is already on another entry, the card lists it
  and says why. Settle it in AdGuard Home, then press **Sync Now**.

## Recent DNS queries

A client's page shows what it looked up lately, from AdGuard Home's query log, with what AdGuard
Home did with each one (blocked, an error, or the address). A link opens the same queries in
AdGuard Home.

The query log has to be on, and AdGuard Home must not hide the end of clients' addresses in it, or
a client's list stays empty. **Test Connection** tells you if either is so.

## If AdGuard Home refuses the login

Five wrong logins make AdGuard Home block the host's address for 15 minutes, and during the block
even the right password is refused. So after a refusal Drawbridge stops asking until you change the
connection, or **Test Connection** or **Sync Now** shows it works. Check the password before you
press them again.

## Remove it

**Remove…** makes Drawbridge forget the address and the password. The names it already added stay
in AdGuard Home until you delete them there.
