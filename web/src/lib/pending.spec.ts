import { describe, expect, it } from 'vitest';
import { changeLines, pending, secondsLeft, setPending } from './pending.svelte';

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
