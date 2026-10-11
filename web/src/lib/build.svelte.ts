import { fetchVersion, type VersionInfo } from './version';

/**
 * The build the server is running (`GET /api/version`), asked for once and shared by everything
 * that names it: the dashboard's footer, and every link to the docs, which open the entry that
 * matches it (`docsUrl`). `info` is undefined until the server has said, or if it can't.
 */
export const build = $state({
	info: undefined as VersionInfo | undefined
});

let asking: Promise<void> | undefined;

/**
 * Asks the server for the build, once however many pages ask, and waits for the answer. After a
 * failure the next call asks again, so a server that was busy once isn't taken for good.
 */
export function loadBuild(fetchFn: typeof fetch = fetch): Promise<void> {
	asking ??= fetchVersion(fetchFn).then(
		(v) => {
			build.info = v;
		},
		() => {
			asking = undefined;
		}
	);
	return asking;
}
