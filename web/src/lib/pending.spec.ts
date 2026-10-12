import { describe, expect, it } from 'vitest';
import {
	changeLines,
	pending,
	secondsLeft,
	setPending,
	setPendingFromResponse
} from './pending.svelte';

describe('secondsLeft', () => {
	it('counts down from when the server said, whatever this clock says the time is', () => {
		expect(secondsLeft(60, 1_000_000, 1_000_000)).toBe(60);
		expect(secondsLeft(60, 1_000_000, 1_000_000 + 12_000)).toBe(48);
	});
	it('rounds up, so it says 1 until the time is up', () => {
		expect(secondsLeft(60, 0, 59_001)).toBe(1);
		expect(secondsLeft(60, 0, 60_000)).toBe(0);
	});
	it('never goes below zero', () => {
		expect(secondsLeft(60, 0, 90_000)).toBe(0);
		expect(secondsLeft(0, 5_000, 5_000)).toBe(0);
	});
});

describe('changeLines', () => {
	it('names each change, sorted', () => {
		expect(changeLines({ mtu: '1420 → 1380', listen_port: '51820 → 51999' })).toEqual([
			'Listen port: 51820 → 51999',
			'Mtu: 1420 → 1380'
		]);
	});
});

describe('setPending', () => {
	it('keeps the change and when it arrived, and clears it', () => {
		const change = {
			changes: { listen_port: '51820 → 51999' },
			expires_at: '2026-10-04T12:01:00Z',
			expires_in: 60,
			actor: 'admin',
			via: 'web' as const
		};
		setPending(change, 123);
		expect(pending.change).toEqual(change);
		expect(pending.receivedAt).toBe(123);
		setPending(undefined, 456);
		expect(pending.change).toBeUndefined();
	});
});

// A phone's tunnel dies with the change that cuts it off, so the answer to that change can arrive
// long after the daemon undid it. The countdown has to start where the server's clock did, not where
// this one saw the answer.
describe('setPendingFromResponse', () => {
	const change = {
		changes: { listen_port: '51820 → 51999' },
		expires_at: '2026-10-12T12:01:00Z',
		expires_in: 60,
		actor: 'admin',
		via: 'web' as const
	};

	it('counts from when the request was sent, which can only understate what is left', () => {
		setPending(undefined, 0);
		expect(setPendingFromResponse(change, 1_000, 1_400)).toBe(true);
		expect(pending.change).toEqual(change);
		expect(pending.receivedAt).toBe(1_000);
		expect(secondsLeft(change.expires_in, pending.receivedAt, 1_400)).toBe(60);
		expect(secondsLeft(change.expires_in, pending.receivedAt, 31_000)).toBe(30);
	});

	it('records nothing for an answer that came after the time was up, and says so', () => {
		setPending(undefined, 5);
		expect(setPendingFromResponse(change, 0, 70_000)).toBe(false);
		expect(pending.change).toBeUndefined();
		expect(pending.receivedAt).toBe(5);
	});

	it('does not replace what the live feed said since with a stale answer', () => {
		const newer = { ...change, changes: { mtu: '1420 → 1380' }, expires_in: 40 };
		setPending(newer, 50_000);
		expect(setPendingFromResponse(change, 0, 70_000)).toBe(false);
		expect(pending.change).toEqual(newer);
	});

	it('takes the moment the time is up as too late', () => {
		setPending(undefined, 0);
		expect(setPendingFromResponse(change, 0, 59_999)).toBe(true);
		setPending(undefined, 0);
		expect(setPendingFromResponse(change, 0, 60_000)).toBe(false);
	});

	it('an answer with nothing waiting clears the bar, however late', () => {
		setPending(change, 0);
		expect(setPendingFromResponse(undefined, 0, 70_000)).toBe(true);
		expect(pending.change).toBeUndefined();
	});
});
