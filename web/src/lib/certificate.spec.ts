import { describe, expect, it } from 'vitest';
import { checkPem, daysLeft, describeValidity } from './certificate';

const cert = { not_before: '2026-09-01T00:00:00Z', not_after: '2026-11-30T12:00:00Z' };

describe('describeValidity', () => {
	it('says when, and how long is left', () => {
		const now = new Date('2026-10-04T12:00:00Z');
		expect(describeValidity(cert, now)).toBe('1 Sep 2026 to 30 Nov 2026 (57 days left)');
		expect(daysLeft(cert, now)).toBe(57);
	});

	it('says "1 day", and "less than a day" at the end', () => {
		expect(describeValidity(cert, new Date('2026-11-29T00:00:00Z'))).toContain('(1 day left)');
		expect(describeValidity(cert, new Date('2026-11-30T06:00:00Z'))).toContain(
			'(less than a day left)'
		);
	});

	it('says so once it has expired', () => {
		expect(describeValidity(cert, new Date('2026-12-01T00:00:00Z'))).toBe(
			'1 Sep 2026 to 30 Nov 2026 (expired)'
		);
		expect(describeValidity(cert, new Date('2026-11-30T12:00:00Z'))).toContain('(expired)');
	});
});

const CERT = '-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n';
const KEY = '-----BEGIN PRIVATE KEY-----\nMIGH\n-----END PRIVATE KEY-----\n';

describe('checkPem', () => {
	it('says nothing about an empty field, which the form requires on its own', () => {
		expect(checkPem('', 'certificate')).toBe('');
		expect(checkPem('  \n', 'key')).toBe('');
	});

	it('accepts what could be fine, a chain or a combined file included', () => {
		expect(checkPem(CERT + CERT, 'certificate')).toBe('');
		expect(checkPem(KEY, 'key')).toBe('');
		expect(
			checkPem('-----BEGIN RSA PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----', 'key')
		).toBe('');
		expect(checkPem(KEY + CERT, 'certificate')).toBe('');
		expect(checkPem(CERT + KEY, 'key')).toBe('');
	});

	it('catches text that is not PEM', () => {
		expect(checkPem('hello', 'certificate')).toContain('start with -----BEGIN CERTIFICATE-----');
		expect(checkPem('hello', 'key')).toContain('start with -----BEGIN PRIVATE KEY-----');
	});

	it('catches the two fields swapped', () => {
		expect(checkPem(KEY, 'certificate')).toContain('That is a private key');
		expect(checkPem(CERT, 'key')).toContain('That is a certificate');
	});

	it('catches a key that has a passphrase', () => {
		const encrypted =
			'-----BEGIN ENCRYPTED PRIVATE KEY-----\nMIIF\n-----END ENCRYPTED PRIVATE KEY-----';
		expect(checkPem(encrypted, 'key')).toContain('protected by a passphrase');
		const legacy =
			'-----BEGIN RSA PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,AB\n\nMIIE\n-----END RSA PRIVATE KEY-----';
		expect(checkPem(legacy, 'key')).toContain('protected by a passphrase');
	});
});
