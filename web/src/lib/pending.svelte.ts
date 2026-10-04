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
