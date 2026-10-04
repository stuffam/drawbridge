/**
 * Two-factor authentication helpers (docs/PLAN.md §6.5). The codes themselves are made and checked
 * on the server; this only prepares what the admin reads and saves.
 */

/** A secret in groups of four, which is easier to read off the screen and to type into an app. */
export function groupSecret(secret: string): string {
	return secret.match(/.{1,4}/g)?.join(' ') ?? secret;
}

/** The text of the file the admin saves their recovery codes in. */
export function recoveryCodesFile(username: string, codes: string[]): string {
	return [
		`Drawbridge recovery codes for ${username}`,
		'',
		'Each code works once, in place of the code from your authenticator app. Keep them',
		'somewhere safe that is not on the phone with the app. Making new codes on the Account',
		'page voids all of these.',
		'',
		...codes,
		''
	].join('\n');
}

/**
 * Whether what was typed looks like a six-digit code from an authenticator app (spaces are
 * allowed, as apps show them), and not a recovery code. Only the login uses it, to say what the
 * field wants.
 */
export function looksLikeAppCode(text: string): boolean {
	return /^\d{6}$/.test(text.replace(/\s+/g, ''));
}

/** At or below this many recovery codes, the Account page asks for new ones. */
export const LOW_RECOVERY_CODES = 3;
