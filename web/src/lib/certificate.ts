import type { Certificate } from './api';
import { formatDay } from './format';

const DAY = 24 * 60 * 60 * 1000;

/** What the admin is told about where the certificate came from. */
export const sourceLabels: Record<Certificate['source'], string> = {
	'self-signed': 'Self-signed by Drawbridge',
	uploaded: 'Installed by you'
};

/** Whole days until the certificate expires; zero or less once it has. */
export function daysLeft(c: Pick<Certificate, 'not_after'>, now: Date): number {
	return Math.floor((Date.parse(c.not_after) - now.getTime()) / DAY);
}

/**
 * When the certificate is valid, with what is left: "1 Sep 2026 to 30 Nov 2026 (57 days left)",
 * or "(expired)" once it has run out.
 */
export function describeValidity(
	c: Pick<Certificate, 'not_before' | 'not_after'>,
	now: Date
): string {
	const from = formatDay(new Date(c.not_before), true);
	const to = formatDay(new Date(c.not_after), true);
	const left = daysLeft(c, now);
	const rest =
		Date.parse(c.not_after) <= now.getTime()
			? 'expired'
			: left < 1
				? 'less than a day left'
				: `${left} ${left === 1 ? 'day' : 'days'} left`;
	return `${from} to ${to} (${rest})`;
}

/**
 * Whether what the admin typed or picked could be a PEM file of the kind: the check that stops a
 * click when a field is plainly wrong, before the server looks properly. `kind` is `certificate`
 * or `key`. It says what is wrong, or nothing when the text may be fine.
 */
export function checkPem(text: string, kind: 'certificate' | 'key'): string {
	const t = text.trim();
	if (!t) return '';
	if (!t.includes('-----BEGIN ')) {
		return `That doesn't look like PEM text: it should start with -----BEGIN ${kind === 'key' ? 'PRIVATE KEY' : 'CERTIFICATE'}-----.`;
	}
	if (kind === 'certificate' && !t.includes('-----BEGIN CERTIFICATE-----')) {
		return t.includes('PRIVATE KEY-----')
			? 'That is a private key. The certificate goes here, and the key in the field below.'
			: 'No certificate in that text.';
	}
	if (kind === 'key' && !t.includes('PRIVATE KEY-----')) {
		return t.includes('-----BEGIN CERTIFICATE-----')
			? 'That is a certificate. The private key goes here.'
			: 'No private key in that text.';
	}
	if (kind === 'key' && (t.includes('ENCRYPTED PRIVATE KEY') || /Proc-Type:.*ENCRYPTED/.test(t))) {
		return 'That key is protected by a passphrase. Remove the passphrase first: openssl pkey -in key.pem -out key-plain.pem';
	}
	return '';
}
