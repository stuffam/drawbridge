import { describe, expect, it } from 'vitest';
import { groupSecret, looksLikeAppCode, recoveryCodesFile } from './totp';

describe('groupSecret', () => {
	it('splits a 32-character secret into eight groups of four', () => {
		expect(groupSecret('GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ')).toBe(
			'GEZD GNBV GY3T QOJQ GEZD GNBV GY3T QOJQ'
		);
	});

	it('keeps a short tail, and an empty secret', () => {
		expect(groupSecret('ABCDEFGHIJ')).toBe('ABCD EFGH IJ');
		expect(groupSecret('')).toBe('');
	});
});

describe('recoveryCodesFile', () => {
	it('names the account and lists one code to a line', () => {
		const text = recoveryCodesFile('admin', ['ABCDE-FGHJK-LMNPQ', 'RSTUV-WXYZ2-34567']);
		expect(text).toContain('recovery codes for admin');
		expect(text).toContain('\nABCDE-FGHJK-LMNPQ\nRSTUV-WXYZ2-34567\n');
		expect(text.endsWith('\n')).toBe(true);
	});
});

describe('looksLikeAppCode', () => {
	it('accepts six digits, as an app shows them', () => {
		expect(looksLikeAppCode('123456')).toBe(true);
		expect(looksLikeAppCode('123 456')).toBe(true);
		expect(looksLikeAppCode(' 007008 ')).toBe(true);
	});

	it('refuses everything else, recovery codes included', () => {
		for (const text of ['', '12345', '1234567', '12345a', 'ABCDE-FGHJK-LMNPQ']) {
			expect(looksLikeAppCode(text)).toBe(false);
		}
	});
});
