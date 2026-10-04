-- Schema version 9: outdated-config tracking (docs/PLAN.md §6.1).
--
-- delivered_hash is the fingerprint (internal/clientconf) of the config the admin last
-- downloaded or showed as a QR code for this client, and delivered_at is when. A client whose
-- current fingerprint differs from delivered_hash holds a config that no longer matches the
-- server, and is flagged "config outdated". Both are empty for a client whose config was never
-- handed out, and for every client that existed before this version: they have no baseline
-- until the next time their config is viewed, so none is flagged outdated by the upgrade.
--
-- The fingerprint is a SHA-256 of the config with the client's public key where its private
-- key would be, so it doesn't depend on a secret and can be computed for a client whose
-- private key the server doesn't keep.

ALTER TABLE clients ADD COLUMN delivered_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE clients ADD COLUMN delivered_at   TEXT;
