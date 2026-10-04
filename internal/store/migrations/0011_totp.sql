-- Schema version 11: TOTP two-factor authentication for the admin account (docs/PLAN.md §6.5, §7).
--
-- totp_secret_enc is the account's TOTP secret, sealed like the other secrets (the purpose names
-- the account, so it can't be swapped for another's). It is set when the admin starts enrolling and
-- stays until 2FA is turned off. totp_enabled_at is NULL while the enrollment waits for the first
-- code from the admin's app, and is the time it was proved: only then does a login ask for a code.
--
-- totp_last_step is the last time step whose code was accepted, so a code can't be used twice
-- inside the window it stays valid for (RFC 6238 §5.2).
--
-- recovery_codes_hash is a JSON array of the SHA-256 hashes (hex) of the recovery codes that
-- haven't been used. The codes are random and long enough that a hash of one can't be guessed
-- backward. A code that is used is removed from the array.

ALTER TABLE users ADD COLUMN totp_secret_enc BLOB;
ALTER TABLE users ADD COLUMN totp_enabled_at TEXT;
ALTER TABLE users ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN recovery_codes_hash TEXT;
