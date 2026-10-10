---
title: Two-Factor Authentication
---

With two-factor authentication on, logging in to the web UI takes the password and a six-digit code
from an authenticator app on your phone. A password that leaks, or that someone watches you type,
isn't enough on its own. Anyone who controls the UI can add a VPN client and so reach your whole
home network, so it's worth the extra ten seconds.

It's off until you turn it on, and it's the only account there is: Drawbridge has one admin.

## What you need

- An authenticator app that makes standard time-based codes (TOTP): Aegis, Google Authenticator,
  1Password, Bitwarden, Authy, the one in your password manager, and most others. Drawbridge uses
  what they all support: SHA-1, six digits, and a new code every 30 seconds.
- **A correct clock on the host.** The code depends on the time. Drawbridge accepts the code from
  the 30 seconds before and after the current one, so a clock a few seconds off is fine, but one
  that's a minute off, or wrong at boot because the host has no battery-backed clock (a Raspberry
  Pi, say), makes every code fail until the clock is set. `drawbridge doctor` has a clock check,
  and its fix is `sudo timedatectl set-ntp true`.

## Turn it on

1. Open the Account page (the person icon in the header, then Settings) and find
   **Two-Factor Authentication**.
2. Press **Turn On…** and type your password again. Whoever turns this on decides what the second
   factor is, so a logged-in browser alone can't do it.
3. Scan the QR code with your app. If it can't scan, type the key shown beside the code into it.
4. Type the code the app shows now, and press **Turn On**. It isn't on until you do, so a mistake
   here can't lock you out.
5. **Save the ten recovery codes it shows.** They're shown once. Each works once, in place of the
   app's code, if you lose your phone. Keep them somewhere that isn't on the phone: a password
   manager's secure note, or paper in a drawer. **Download** saves them as a text file.

Turning it on logs out your other browsers, as changing the password does. The one you turned it
on in stays logged in.

## Log in

Type the username and password as before. Drawbridge then asks for the code. Type the six digits
the app shows (the space in "123 456" is fine). A wrong code counts like a wrong password: five
free tries, then a wait that doubles up to 15 minutes, for the account and for the address you're
coming from.

A code works once. If you log in, log out, and log in again within the same 30 seconds, the second
login needs the next code, which the app shows when its timer rolls over.

Lost the phone? Type a recovery code in the same box. It works once, and the Account page shows how
many are left; it suggests making new ones when three or fewer remain.

## Recovery codes

**New Recovery Codes…** on the Account page makes ten new ones, and the old ones, used or not,
stop working. It takes your password and a code (from the app, or a recovery code), like turning
2FA off does. Drawbridge keeps only a hash of each code, so it can't show you the ones you have.
If you lose them, make new ones while you still have the app.

## Turn it off

**Turn Off…** takes your password and a code (from the app, or an unused recovery code), so a
stolen login can't take the second factor away. Turning it off forgets the secret and the recovery
codes, and logs out your other browsers. Turning it on again makes a new secret: add it to the app
as a new entry.

## Lost the phone and the recovery codes

On the host:

```bash
sudo drawbridge admin disable-2fa
```

It turns two-factor authentication off for the admin account, logs out all of its sessions, and
lifts any lockout from failed logins. Log in with the password, and turn it on again with the new
phone. Whoever can run it can already run `drawbridge admin reset-password`: it needs root or
membership in the `drawbridge` group, which is the same authority as everything else the command
line does. It says so when 2FA was already off, and changes nothing then.

`drawbridge admin reset-password` doesn't turn 2FA off: forgetting the password and losing the
phone are separate problems. After a reset the new password still needs a code.

## What it protects, and what it doesn't

- It protects **the login**: the web UI's password step. The password and the code are both
  checked before a session starts, and every failure of either counts toward the same lockout.
- **[Read-only API tokens](api-tokens.md)** aren't asked for a code: a dashboard can't type
  one. A token can't change anything, and you can revoke it on the Account page. Making a token
  takes your password again, but not a code.
- **Sessions** are checked once, when they start. Turning 2FA on or off logs out your other
  browsers, so none outlives the change; after that a session lasts as usual, an hour idle or
  twelve hours at most.
- The command line on the host doesn't use the web login, so it doesn't ask for a code either.
  Anyone with a root shell on the host can already read the database and the key.
- The host's own secret key seals the TOTP secret in the database, as it does the private keys. A
  copy of the database alone can't make codes; **a [backup](backup-restore.md) holds both**, and
  is encrypted for that reason. Restoring one onto a new host brings 2FA with it: your
  authenticator app keeps working, and the recovery codes you haven't used still do.

## What the log shows

The log on the Logs page, and `drawbridge events`, say what happened and never what the secret or
a code was:

| Event | When |
|---|---|
| `auth.totp_enabled` | 2FA was turned on |
| `auth.totp_disabled` | it was turned off, in the browser or by `admin disable-2fa` |
| `auth.totp_failed` | a wrong password or code when turning it on or off, or making new codes |
| `auth.login_failed` | a failed login; the details say `wrong code` when the password was right |
| `auth.recovery_code_used` | a recovery code was used, and how many are left |
| `auth.recovery_codes_renewed` | new recovery codes were made |

A burst of `wrong code` failures with the right password means someone has your password. Change it
(Account page), and make new recovery codes.

## Troubleshooting

- **"that code is wrong, or has been used already", and the app is right.** Check the host's clock
  (`timedatectl`; `drawbridge doctor`). If it's right, wait for the app's code to change: a code
  you've just used is spent, and the next one comes when the app's timer rolls over.
- **The wait before another try is long.** Wrong codes are limited like wrong passwords. The wait
  ends by itself, and `sudo drawbridge admin disable-2fa` or `reset-password` lifts it at once.
- **The app has several Drawbridge entries.** Each time you turn 2FA on, the secret is new and the
  old entry stops working. Delete the old ones.
