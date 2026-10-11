import { beforeEach, describe, expect, it, vi } from 'vitest';

const info = {
	version: 'v0.1.3',
	commit: 'abc1234',
	date: '2026-10-07T21:15:46Z',
	docs: 'https://stuffam.github.io/drawbridge/v0.1/'
};

function respond(status: number, body: unknown) {
	return async () => new Response(JSON.stringify(body), { status });
}

describe('loadBuild', () => {
	// The module remembers that it has asked, so each test starts with a new one.
	beforeEach(() => vi.resetModules());

	it('asks the server which build it is, and shares the answer', async () => {
		const { build, loadBuild } = await import('./build.svelte');
		expect(build.info).toBeUndefined();
		await loadBuild(respond(200, info));
		expect(build.info).toEqual(info);
	});

	it('asks once, however many pages ask', async () => {
		const { loadBuild } = await import('./build.svelte');
		const fetchFn = vi.fn(respond(200, info));
		await Promise.all([loadBuild(fetchFn), loadBuild(fetchFn)]);
		await loadBuild(fetchFn);
		expect(fetchFn).toHaveBeenCalledTimes(1);
	});

	it('leaves the build unknown when the server cannot say, and asks again next time', async () => {
		const { build, loadBuild } = await import('./build.svelte');
		await loadBuild(respond(503, {}));
		expect(build.info).toBeUndefined();

		const fetchFn = vi.fn(respond(200, info));
		await loadBuild(fetchFn);
		expect(fetchFn).toHaveBeenCalledTimes(1);
		expect(build.info).toEqual(info);
	});
});
