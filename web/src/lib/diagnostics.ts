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
