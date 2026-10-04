-- Schema version 10: a settings change on probation (safe apply, docs/PLAN.md §4.3).
--
-- A change that could cut the admin off (the listen port, removing an admin-UI source) from the
-- web UI is applied at once and undone unless the admin keeps it before the deadline. This holds
-- it while it waits: one row at most, written in the same transaction as the settings, and
-- deleted when the change is kept or undone. It's in the database, not in the daemon's memory,
-- so a restart or a reboot inside the window still undoes it.
--
-- previous is the settings before the change as JSON, without the server's private key, which
-- is kept apart in previous_key_enc, sealed like server.private_key_enc: the key is never
-- written unencrypted. changes is {"setting": "old → new"} for the admin to read.

CREATE TABLE pending_apply (
	id               INTEGER PRIMARY KEY CHECK (id = 1),
	created_at       TEXT NOT NULL,
	deadline         TEXT NOT NULL,
	actor            TEXT NOT NULL,
	via              TEXT NOT NULL,
	source_ip        TEXT NOT NULL DEFAULT '',
	changes          TEXT NOT NULL,
	previous         TEXT NOT NULL,
	previous_key_enc BLOB NOT NULL
);
