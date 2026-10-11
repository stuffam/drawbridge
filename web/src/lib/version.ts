import { formatDay } from './format';

/** Drawbridge's repository, which the footer's name links to. */
export const repoUrl = 'https://github.com/stuffam/drawbridge';

/** Version information that the Go server reports at `/api/version`. */
export interface VersionInfo {
	/** The version with its lowercase v, such as "v1.2.3". */
	version: string;
	/** The short git commit the server was built from. */
	commit: string;
	/** When that commit was made, as an RFC 3339 time in UTC. Empty when the build couldn't tell. */
	date: string;
	/** The start of the documentation that matches this build, ending in a slash. */
	docs: string;
}

/**
 * Formats version information for people: "v1.2.3 (abc1234, 7 Oct 2026)", the same text as
 * `drawbridge version` (`internal/version`). The day is the commit's, in the browser's time zone
 * like every other date in the app, and is left out when the build has none.
 */
export function formatVersion(info: VersionInfo): string {
	const d = new Date(info.date);
	const day = Number.isNaN(d.getTime()) ? '' : `, ${formatDay(d, true)}`;
	return `${info.version} (${info.commit}${day})`;
}

/** What a released version looks like, and nothing that could change an address. */
const releaseVersion = /^v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/;
/** The version of a build made between releases (`internal/version`'s default, with its v). */
const devVersion = 'v0.0.0-dev';
/** What a short or full git commit looks like. A dirty build's "-dirty" suffix isn't one. */
const gitCommit = /^[0-9a-f]{7,40}$/;

/**
 * Where on GitHub to read about this build: its release page when it was built from a release's
 * tag, otherwise its commit (a build made between releases has no release page), and nowhere when
 * the build can't say, as for a dirty tree. Both values come from the server, and neither goes
 * into an address unless it is the shape it should be.
 */
export function versionLink(info: VersionInfo): string | undefined {
	if (info.version !== devVersion && releaseVersion.test(info.version)) {
		return `${repoUrl}/releases/tag/${info.version}`;
	}
	if (gitCommit.test(info.commit)) {
		return `${repoUrl}/commit/${info.commit}`;
	}
	return undefined;
}

/** What the server's docs address looks like: an entry of the docs site, for a minor version or dev. */
const docsBase = /^https:\/\/stuffam\.github\.io\/drawbridge\/(?:dev|v\d+\.\d+)\/$/;
/** The docs of the latest release, where a page is until the server has said, or if it can't. */
const latestDocs = 'https://stuffam.github.io/drawbridge/latest/';

/**
 * The address of a page of the documentation (`guides/backup-restore/`) that matches this build:
 * the docs of its minor version for a release, and the development docs for a pre-release or a
 * build between releases, which is the server's call (`internal/version.DocsURL`, so a link here
 * and one in the doctor's hints can't name different versions). The address comes from the server
 * and goes into a link only if it is an entry of the docs site, in the form the server sends it.
 */
export function docsUrl(info: VersionInfo | undefined, page: string): string {
	return (info !== undefined && docsBase.test(info.docs) ? info.docs : latestDocs) + page;
}

/** Fetches the server's version information. */
export async function fetchVersion(fetchFn: typeof fetch = fetch): Promise<VersionInfo> {
	const res = await fetchFn('/api/version');
	if (!res.ok) {
		throw new Error(`GET /api/version failed with status ${res.status}`);
	}
	const body: unknown = await res.json();
	if (!isVersionInfo(body)) {
		throw new Error('GET /api/version returned an unexpected body');
	}
	return { version: body.version, commit: body.commit, date: body.date, docs: body.docs };
}

function isVersionInfo(value: unknown): value is VersionInfo {
	if (typeof value !== 'object' || value === null) {
		return false;
	}
	const { version, commit, date, docs } = value as Record<string, unknown>;
	return (
		typeof version === 'string' &&
		typeof commit === 'string' &&
		typeof date === 'string' &&
		typeof docs === 'string'
	);
}
