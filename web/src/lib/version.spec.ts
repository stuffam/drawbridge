import { describe, expect, it } from 'vitest';
import { fetchVersion, formatVersion, repoUrl, versionLink } from './version';

function fakeFetch(status: number, body: unknown): typeof fetch {
	return async () => new Response(JSON.stringify(body), { status });
}

// Built from local parts, so the day it names is the same in whatever time zone the tests run.
const stamp = new Date(2026, 9, 7, 21, 15, 46).toISOString();

describe('formatVersion', () => {
	it('puts the version, the commit, and the day it was made together', () => {
		expect(formatVersion({ version: 'v1.2.3', commit: 'abc1234', date: stamp })).toBe(
			'v1.2.3 (abc1234, 7 Oct 2026)'
		);
	});

	it('leaves the day out when the build has none', () => {
		expect(formatVersion({ version: 'v1.2.3', commit: 'abc1234', date: '' })).toBe(
			'v1.2.3 (abc1234)'
		);
	});

	it('leaves out a day it cannot read, rather than printing it', () => {
		expect(formatVersion({ version: 'v1.2.3', commit: 'abc1234', date: 'yesterday' })).toBe(
			'v1.2.3 (abc1234)'
		);
	});
});

describe('versionLink', () => {
	const make = (version: string, commit: string) => ({ version, commit, date: stamp });

	it('goes to the release page of a tagged build', () => {
		expect(versionLink(make('v0.1.0-rc.2', '4e52c6d'))).toBe(`${repoUrl}/releases/tag/v0.1.0-rc.2`);
		expect(versionLink(make('v1.0.0', '4e52c6d-dirty'))).toBe(`${repoUrl}/releases/tag/v1.0.0`);
	});

	it('goes to the commit when the build is between releases, which have no page', () => {
		expect(versionLink(make('v0.0.0-dev', '4e52c6d'))).toBe(`${repoUrl}/commit/4e52c6d`);
	});

	it('goes to the commit when the build is some commits past a release, which has no page', () => {
		expect(versionLink(make('v0.1.0-rc.2+61', '29caacc'))).toBe(`${repoUrl}/commit/29caacc`);
		expect(versionLink(make('v0.1.0+2', '29caacc'))).toBe(`${repoUrl}/commit/29caacc`);
		expect(versionLink(make('v0.1.0+2', '29caacc-dirty'))).toBeUndefined();
	});

	it('goes nowhere when the build has no commit that GitHub would know', () => {
		expect(versionLink(make('v0.0.0-dev', 'unknown'))).toBeUndefined();
		expect(versionLink(make('v0.0.0-dev', '4e52c6d-dirty'))).toBeUndefined();
	});

	it('keeps anything but a version and a commit out of the address', () => {
		expect(versionLink(make('v1.0.0/../x?y=1', 'unknown'))).toBeUndefined();
		expect(versionLink(make('v0.0.0-dev', '4e52c6d/../x'))).toBeUndefined();
		expect(versionLink(make('v1.0.0 "onmouseover=', '4e52c6d'))).toBe(`${repoUrl}/commit/4e52c6d`);
	});

	it('takes only a version with its v, the way the server sends it', () => {
		expect(versionLink(make('1.0.0', 'unknown'))).toBeUndefined();
	});
});

describe('fetchVersion', () => {
	it('returns the version, commit, and date', async () => {
		const body = { version: 'v0.0.0-dev', commit: 'unknown', date: '' };
		expect(await fetchVersion(fakeFetch(200, body))).toEqual(body);
	});

	it('rejects an error status', async () => {
		await expect(fetchVersion(fakeFetch(503, {}))).rejects.toThrow('status 503');
	});

	it('rejects a body without the expected fields', async () => {
		await expect(fetchVersion(fakeFetch(200, { version: 1 }))).rejects.toThrow('unexpected body');
		await expect(fetchVersion(fakeFetch(200, { version: 'v1', commit: 'c' }))).rejects.toThrow(
			'unexpected body'
		);
	});
});
