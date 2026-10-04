import { afterEach, describe, expect, it, vi } from 'vitest';
import { api, ApiError, onUnauthorized, setFetch } from './api';

function respond(status: number, body?: unknown, headers: Record<string, string> = {}) {
	const init: ResponseInit = { status, headers: { ...headers } };
	if (body !== undefined && typeof body !== 'string') {
		(init.headers as Record<string, string>)['Content-Type'] = 'application/json';
		return new Response(JSON.stringify(body), init);
	}
	return new Response(body as string | undefined, init);
}

afterEach(() => {
	setFetch((...args) => fetch(...args));
	onUnauthorized(undefined);
});

describe('api', () => {
	it('sends the CSRF header, and JSON bodies', async () => {
		const fetchFn = vi.fn(async () => respond(201, { client: { id: 'c1' } }));
		setFetch(fetchFn);
		await api.addClient('phone');
		expect(fetchFn).toHaveBeenCalledWith('/api/clients', {
			method: 'POST',
			headers: { 'X-Drawbridge': '1', 'Content-Type': 'application/json' },
			body: '{"name":"phone"}',
			credentials: 'same-origin'
		});
	});

	it('reports the server error message and Retry-After', async () => {
		setFetch(async () =>
			respond(429, { error: 'too many failed attempts' }, { 'Retry-After': '4' })
		);
		const err = await api.login('admin', 'x').catch((e: unknown) => e);
		expect(err).toBeInstanceOf(ApiError);
		expect((err as ApiError).status).toBe(429);
		expect((err as ApiError).message).toBe('too many failed attempts');
		expect((err as ApiError).retryAfter).toBe(4);
	});

	it('calls the unauthorized handler, except for a failed login', async () => {
		const handler = vi.fn();
		onUnauthorized(handler);
		setFetch(async () => respond(401, { error: 'not logged in' }));
		await expect(api.clients()).rejects.toThrow('not logged in');
		expect(handler).toHaveBeenCalledTimes(1);
		await expect(api.login('admin', 'wrong')).rejects.toThrow();
		expect(handler).toHaveBeenCalledTimes(1);
	});

	it('returns plain text for configs, and nothing for 204', async () => {
		setFetch(async () =>
			respond(200, '[Interface]\n', { 'Content-Type': 'text/plain; charset=utf-8' })
		);
		expect(await api.clientConfig('c1')).toBe('[Interface]\n');
		setFetch(async () => new Response(null, { status: 204 }));
		expect(await api.logout()).toBeUndefined();
	});

	it('builds the event query', async () => {
		const fetchFn = vi.fn(async () => respond(200, []));
		setFetch(fetchFn);
		await api.events({ category: 'admin', before: 42, client: '' });
		await api.events();
		expect(fetchFn.mock.calls.map((c) => (c as unknown[])[0])).toEqual([
			'/api/events?category=admin&before=42',
			'/api/events'
		]);
	});

	it('asks for kinds and times, and for the whole log as CSV', async () => {
		const fetchFn = vi.fn(async () => respond(200, '', { 'Content-Type': 'text/csv' }));
		setFetch(fetchFn);
		const from = '2026-09-30T12:00:00.000Z';
		await api.events({ kind: 'client.connected', from, limit: 50 });
		// A CSV is the whole log, so a page's limit and cursor are left out.
		await api.eventsCsv({ kind: 'client.connected', from, limit: 50, before: 9 });
		await api.eventsCsv();
		expect(fetchFn.mock.calls.map((c) => (c as unknown[])[0])).toEqual([
			'/api/events?kind=client.connected&from=2026-09-30T12%3A00%3A00.000Z&limit=50',
			'/api/events?kind=client.connected&from=2026-09-30T12%3A00%3A00.000Z&format=csv',
			'/api/events?format=csv'
		]);
	});

	it('builds the traffic and session-history requests', async () => {
		const fetchFn = vi.fn(async () => respond(200, []));
		setFetch(fetchFn);
		await api.traffic();
		await api.traffic('7d');
		await api.clientTraffic('c1');
		await api.clientTraffic('c1', '90d');
		await api.clientTraffic('c1', '1m');
		await api.trafficByClient();
		await api.trafficByClient('12h');
		await api.clientSessions('c1');
		await api.clientSessions('c1', '2026-09-28T00:00:00Z', 10);
		expect(fetchFn.mock.calls.map((c) => (c as unknown[])[0])).toEqual([
			'/api/traffic?range=24h',
			'/api/traffic?range=7d',
			'/api/clients/c1/traffic?range=24h',
			'/api/clients/c1/traffic?range=90d',
			'/api/clients/c1/traffic?range=1m',
			'/api/traffic/clients?range=24h',
			'/api/traffic/clients?range=12h',
			'/api/clients/c1/sessions',
			'/api/clients/c1/sessions?before=2026-09-28T00%3A00%3A00Z&limit=10'
		]);
	});

	it('downloads a backup with the password and passphrase, and names the file as the server did', async () => {
		const file = 'drawbridge-backup\nxxxxxxxx';
		const fetchFn = vi.fn(async () =>
			respond(200, file, {
				'Content-Type': 'application/octet-stream',
				'Content-Length': String(file.length),
				'Content-Disposition': 'attachment; filename="drawbridge-20261004-143000.backup"'
			})
		);
		setFetch(fetchFn);
		const got = await api.downloadBackup('the password', 'a long enough passphrase');
		expect(fetchFn).toHaveBeenCalledWith('/api/system/backup', {
			method: 'POST',
			headers: { 'X-Drawbridge': '1', 'Content-Type': 'application/json' },
			body: '{"password":"the password","passphrase":"a long enough passphrase"}',
			credentials: 'same-origin'
		});
		expect(got.name).toBe('drawbridge-20261004-143000.backup');
		expect(await got.blob.text()).toBe(file);
	});

	it('reports why a backup was refused, and sends a lapsed session to the login', async () => {
		const handler = vi.fn();
		onUnauthorized(handler);
		setFetch(async () => respond(400, { error: 'the current password is wrong' }));
		const err = await api.downloadBackup('x', 'a long enough passphrase').catch((e: unknown) => e);
		expect(err).toBeInstanceOf(ApiError);
		expect((err as ApiError).message).toBe('the current password is wrong');
		expect(handler).not.toHaveBeenCalled();

		setFetch(async () =>
			respond(429, { error: 'too many failed attempts' }, { 'Retry-After': '30' })
		);
		const limited = await api
			.downloadBackup('x', 'a long enough passphrase')
			.catch((e: unknown) => e);
		expect((limited as ApiError).retryAfter).toBe(30);

		setFetch(async () => respond(401, { error: 'not logged in' }));
		await expect(api.downloadBackup('x', 'a long enough passphrase')).rejects.toThrow(
			'not logged in'
		);
		expect(handler).toHaveBeenCalledTimes(1);
	});

	it('refuses a backup that arrived short, or empty, instead of saving it', async () => {
		const headers = { 'Content-Length': '100', 'Content-Disposition': 'attachment; filename="x"' };
		setFetch(async () => respond(200, 'only part of it', headers));
		const short = await api
			.downloadBackup('p', 'a long enough passphrase')
			.catch((e: unknown) => e);
		expect(short).toBeInstanceOf(ApiError);
		expect((short as ApiError).message).toContain('cut short');

		setFetch(async () => respond(200, '', { 'Content-Length': '0' }));
		await expect(api.downloadBackup('p', 'a long enough passphrase')).rejects.toThrow('cut short');

		// A body that fails while it's read, as a dropped connection does.
		const broken = new ReadableStream({
			pull(c) {
				c.error(new TypeError('network error'));
			}
		});
		setFetch(async () => new Response(broken, { status: 200, headers }));
		await expect(api.downloadBackup('p', 'a long enough passphrase')).rejects.toThrow('cut short');
	});

	it('reads the snapshots', async () => {
		const fetchFn = vi.fn(async () => respond(200, { dir: '/d', nightly: true, snapshots: [] }));
		setFetch(fetchFn);
		expect(await api.snapshots()).toEqual({ dir: '/d', nightly: true, snapshots: [] });
		expect((fetchFn.mock.calls[0] as unknown[])[0]).toBe('/api/system/snapshots');
	});
});
