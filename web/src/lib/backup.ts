// What the System page's backup form checks before it asks the server, and how it names what comes
// back. The server checks all of it again (internal/backup); these only save a round trip, and
// catch the one mistake it can't: a passphrase typed twice differently, which would make a backup
// nobody can open.

/** The shortest passphrase the server takes, in characters (`MinPassphrase` in internal/backup). */
export const MIN_PASSPHRASE = 12;

/** What's wrong with a passphrase and its confirmation, or an empty string when they're good. */
export function checkPassphrase(passphrase: string, again: string): string {
	// The server counts characters, not UTF-16 units, so spread to count the same way.
	if ([...passphrase].length < MIN_PASSPHRASE) {
		return `The passphrase needs at least ${MIN_PASSPHRASE} characters.`;
	}
	if (passphrase !== again) {
		return "The two passphrases don't match.";
	}
	return '';
}

/**
 * The file name in a Content-Disposition header such as `attachment; filename="drawbridge-1.backup"`.
 * The server names its backups, so a header that says anything else gets a name of ours: the file
 * name comes from a response, and goes to the user's disk.
 */
export function backupFileName(disposition: string | null): string {
	const m = /filename="(drawbridge-\d{8}-\d{6}\.backup)"/.exec(disposition ?? '');
	return m ? m[1] : 'drawbridge.backup';
}

/** What a snapshot is, for a list of them. */
export function snapshotLabel(s: { kind: string; schema?: number }): string {
	if (s.kind === 'pre-migration') {
		return s.schema ? `Before an upgrade (database version ${s.schema})` : 'Before an upgrade';
	}
	return 'Nightly';
}
