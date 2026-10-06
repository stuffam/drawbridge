import { execFileSync } from 'node:child_process';
import tailwindcss from '@tailwindcss/vite';
import type { ProxyOptions } from 'vite';
import { defineConfig } from 'vitest/config';
import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';

// The Go server (`drawbridge serve`) listens on 51821, over HTTPS with a self-signed
// certificate. `npm run dev` proxies the API to it, so the dev server on Vite's default
// port 5173 behaves like the embedded app. The proxy drops the Origin header, which names
// the dev server rather than the Go server and so would fail the CSRF check.
const goServer: ProxyOptions = {
	target: 'https://127.0.0.1:51821',
	secure: false,
	configure: (proxy) => {
		proxy.on('proxyReq', (req) => req.removeHeader('origin'));
	}
};

// SvelteKit names each build with the time it was made, and that name ends up in the entry chunks,
// so two builds of the same source differ in every file that imports one. The commit makes the
// build a function of its source, which a release's reproducible package needs (docs/PLAN.md
// §10). The app doesn't read the name; SvelteKit only compares it to spot a new deployment.
function buildName(): string {
	try {
		return execFileSync('git', ['rev-parse', 'HEAD'], {
			encoding: 'utf8',
			stdio: ['ignore', 'pipe', 'ignore']
		}).trim();
	} catch {
		return 'unknown';
	}
}

export default defineConfig({
	plugins: [
		tailwindcss(),
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},
			// A single-page app: the Go server returns 200.html for every path that isn't a
			// file, and the client-side router takes over (internal/api, internal/webui).
			adapter: adapter({ fallback: '200.html' }),
			version: { name: buildName() }
		})
	],
	server: {
		proxy: {
			'/api': goServer,
			'/healthz': goServer
		}
	},
	test: {
		expect: { requireAssertions: true },
		projects: [
			{
				extends: './vite.config.ts',
				test: {
					name: 'server',
					environment: 'node',
					include: ['src/**/*.{test,spec}.{js,ts}'],
					exclude: ['src/**/*.svelte.{test,spec}.{js,ts}']
				}
			}
		]
	}
});
