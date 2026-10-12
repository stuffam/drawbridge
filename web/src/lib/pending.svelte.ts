import type { PendingChange } from './api';

/**
 * The settings change waiting to be kept (safe apply, docs/PLAN.md §4.3), shared by the page that
 * made it and the bar that every page shows. It comes from the live feed, from the response to
 * the change, or from asking, whichever is newest.
 */
export const pending = $state({
	change: undefined as PendingChange | undefined,
	/** When `change` arrived (ms since the epoch): `expires_in` counts down from here. */
	receivedAt: 0
});

/** Records the change waiting, or none. */
export function setPending(change: PendingChange | undefined, now = Date.now()) {
	pending.change = change;
	pending.receivedAt = now;
}

/**
 * Records what a response to a settings change says is waiting, counting its `expires_in` from
 * when the request was sent. The server made that number after the request went out and before the
 * answer got back, so counting from the send can only understate what is left, by the request's
 * own trip. A change that cuts the admin's tunnel is answered only once the tunnel is back, long
 * after the daemon undid it, and counting from the answer's arrival would start a fresh countdown
 * for a change that is gone. When the time is already up, nothing is recorded and this returns
 * false: the caller asks the server what is in place instead (`settledResult`).
 */
export function setPendingFromResponse(
	change: PendingChange | undefined,
	sentAt: number,
	now = Date.now()
): boolean {
	if (change && secondsLeft(change.expires_in, sentAt, now) === 0) return false;
	setPending(change, change ? sentAt : now);
	return true;
}

/**
 * Whole seconds left of `expiresIn`, which was true at `receivedAt`, as of `now`. It rounds up,
 * so the bar says 1 until the time is truly up, and never goes below zero.
 */
export function secondsLeft(expiresIn: number, receivedAt: number, now: number): number {
	return Math.max(0, Math.ceil((receivedAt + expiresIn * 1000 - now) / 1000));
}

/** What changed, one line each: "Listen port: 51820 → 51999". */
export function changeLines(changes: Record<string, string>): string[] {
	return Object.keys(changes)
		.sort()
		.map((k) => {
			const name = k.replaceAll('_', ' ');
			return `${name[0].toUpperCase()}${name.slice(1)}: ${changes[k]}`;
		});
}
