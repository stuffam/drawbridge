import { describe, expect, it } from 'vitest';
import { MIN_PASSPHRASE, backupFileName, checkPassphrase, snapshotLabel } from './backup';

describe('checkPassphrase', () => {
	it('takes a passphrase of the minimum length that was typed twice the same', () => {
		const p = 'x'.repeat(MIN_PASSPHRASE);
		expect(checkPassphrase(p, p)).toBe('');
		expect(checkPassphrase('correct horse battery', 'correct horse battery')).toBe('');
	});

	it('refuses a short one, whatever the confirmation says', () => {
		const short = 'x'.repeat(MIN_PASSPHRASE - 1);
		expect(checkPassphrase(short, short)).toBe('The passphrase needs at least 12 characters.');
		expect(checkPassphrase('', '')).toContain('at least 12');
	});

	it('refuses two that differ, even by a letter or a space', () => {
		expect(checkPassphrase('correct horse battery', 'correct horse batterz')).toBe(
			"The two passphrases don't match."
		);
		expect(checkPassphrase('correct horse battery', 'correct horse battery ')).toContain('match');
	});

	it('counts characters the way the server does, not UTF-16 units', () => {
		// Eleven emoji are 22 UTF-16 units and 11 characters: too short. Twelve are enough.
		const eleven = '🔑'.repeat(11);
		expect(checkPassphrase(eleven, eleven)).toContain('at least 12');
		const twelve = '🔑'.repeat(12);
		expect(checkPassphrase(twelve, twelve)).toBe('');
	});
});

describe('backupFileName', () => {
	it("uses the server's name for the file", () => {
		expect(backupFileName('attachment; filename="drawbridge-20261004-143000.backup"')).toBe(
			'drawbridge-20261004-143000.backup'
		);
	});

	it('names a file itself when the header is missing or says something else', () => {
		expect(backupFileName(null)).toBe('drawbridge.backup');
		expect(backupFileName('attachment')).toBe('drawbridge.backup');
		// A name that could go somewhere it shouldn't is never used.
		for (const bad of [
			'attachment; filename="../../etc/cron.d/x"',
			'attachment; filename="drawbridge-20261004-143000.backup/../x"',
			'attachment; filename="evil.sh"'
		]) {
			expect(backupFileName(bad)).toBe('drawbridge.backup');
		}
	});
});

describe('snapshotLabel', () => {
	it('says what each kind of snapshot is', () => {
		expect(snapshotLabel({ kind: 'nightly' })).toBe('Nightly');
		expect(snapshotLabel({ kind: 'pre-migration', schema: 4 })).toBe(
			'Before an upgrade (database version 4)'
		);
		expect(snapshotLabel({ kind: 'pre-migration' })).toBe('Before an upgrade');
	});
});
