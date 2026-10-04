import type { CheckStatus, DiagnosticCheck } from './api';

export const statusLabels: Record<CheckStatus, string> = {
	pass: 'Pass',
	warn: 'Warning',
	fail: 'Failed',
	skip: 'Skipped'
};

/** A warning or a failure is what the admin has to look at; a hint goes with it. */
export function needsAttention(c: DiagnosticCheck): boolean {
	return c.status === 'warn' || c.status === 'fail';
}

/**
 * The checks the dashboard raises: the warnings and the failures, except the ones it already
 * says something fresher about. The dashboard knows the tunnel's state from the live feed and
 * the endpoint from the settings it loaded, and says so in a banner of its own, so a check made
 * a few minutes ago that disagrees (or only repeats) doesn't get a second one.
 */
export function dashboardChecks(
	checks: DiagnosticCheck[],
	{ endpointSet }: { endpointSet: boolean }
): DiagnosticCheck[] {
	return checks.filter((c) => {
		if (!needsAttention(c) || c.id === 'tunnel') return false;
		// An endpoint that isn't set is the dashboard's own banner; one that doesn't resolve is not.
		return c.id !== 'endpoint' || endpointSet;
	});
}

/** "1 check needs attention", "3 checks need attention". */
export function attentionHeadline(count: number): string {
	return count === 1 ? '1 check needs attention' : `${count} checks need attention`;
}

/**
 * The checks in a sentence: "All 13 checks passed.", or the counts that aren't zero, worst
 * first, "1 failed, 2 warnings, 10 passed". A skipped check (one the daemon couldn't run)
 * isn't a pass, so it's named even when everything else is fine.
 */
export function summarize(checks: DiagnosticCheck[]): string {
	if (checks.length === 0) return '';
	const n = (status: CheckStatus) => checks.filter((c) => c.status === status).length;
	const fail = n('fail');
	const warn = n('warn');
	const pass = n('pass');
	const skip = n('skip');
	if (pass === checks.length) return `All ${pass} checks passed.`;
	const parts = [
		fail > 0 ? `${fail} failed` : '',
		warn > 0 ? `${warn} ${warn === 1 ? 'warning' : 'warnings'}` : '',
		skip > 0 ? `${skip} skipped` : '',
		pass > 0 ? `${pass} passed` : ''
	];
	return parts.filter(Boolean).join(', ') + '.';
}
