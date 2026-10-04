import { describe, expect, it } from 'vitest';
import type { CheckStatus, DiagnosticCheck } from './api';
import { attentionHeadline, dashboardChecks, needsAttention, summarize } from './diagnostics';

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

describe('dashboardChecks', () => {
	const named = (id: string, status: CheckStatus): DiagnosticCheck => ({
		id,
		name: id,
		status,
		detail: ''
	});
	const ids = (cs: DiagnosticCheck[]) => cs.map((c) => c.id);

	it('keeps the warnings and the failures, in order, and drops the rest', () => {
		const got = dashboardChecks(
			[
				named('forwarding', 'fail'),
				named('uplink', 'pass'),
				named('time-sync', 'warn'),
				named('disk-space', 'skip')
			],
			{ endpointSet: true }
		);
		expect(ids(got)).toEqual(['forwarding', 'time-sync']);
	});

	it('leaves the tunnel to the dashboard, which knows it fresher', () => {
		const got = dashboardChecks([named('tunnel', 'fail'), named('kernel-module', 'warn')], {
			endpointSet: true
		});
		expect(ids(got)).toEqual(['kernel-module']);
	});

	it('leaves an endpoint that is not set to the dashboard, but not one that fails to resolve', () => {
		const checks = [named('endpoint', 'fail')];
		expect(dashboardChecks(checks, { endpointSet: false })).toEqual([]);
		expect(ids(dashboardChecks(checks, { endpointSet: true }))).toEqual(['endpoint']);
	});

	it('is empty when everything is fine', () => {
		expect(dashboardChecks(checks('pass', 'pass', 'skip'), { endpointSet: true })).toEqual([]);
		expect(dashboardChecks([], { endpointSet: true })).toEqual([]);
	});
});

describe('attentionHeadline', () => {
	it('counts them', () => {
		expect(attentionHeadline(1)).toBe('1 check needs attention');
		expect(attentionHeadline(3)).toBe('3 checks need attention');
	});
});
