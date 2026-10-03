import { describe, expect, it } from 'vitest';
import type { CheckStatus, DiagnosticCheck } from './api';
import { needsAttention, summarize } from './diagnostics';

const checks = (...statuses: CheckStatus[]): DiagnosticCheck[] =>
	statuses.map((status, i) => ({ id: `c${i}`, name: `Check ${i}`, status, detail: '' }));

describe('summarize', () => {
	it('says nothing before there are results', () => {
		expect(summarize([])).toBe('');
	});

	it('says so when everything passed', () => {
		expect(summarize(checks('pass', 'pass', 'pass'))).toBe('All 3 checks passed.');
	});

	it('lists what is not zero, worst first', () => {
		expect(summarize(checks('pass', 'warn', 'fail', 'pass', 'warn', 'skip'))).toBe(
			'1 failed, 2 warnings, 1 skipped, 2 passed.'
		);
	});

	it("doesn't call a skipped check a pass", () => {
		expect(summarize(checks('pass', 'pass', 'skip'))).toBe('1 skipped, 2 passed.');
	});

	it('says "1 warning", not "1 warnings"', () => {
		expect(summarize(checks('warn', 'pass'))).toBe('1 warning, 1 passed.');
	});
});

describe('needsAttention', () => {
	it('is the warnings and the failures', () => {
		const got = checks('pass', 'warn', 'fail', 'skip').map(needsAttention);
		expect(got).toEqual([false, true, true, false]);
	});
});
