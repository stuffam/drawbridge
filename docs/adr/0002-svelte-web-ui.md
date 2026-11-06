# ADR 0002: Svelte 5 and SvelteKit for the web UI

- **Status:** Accepted (2026-09-26)
- **Plan reference:** docs/PLAN.md §3, D2

## Context

The web UI is an admin app behind a login, used from a phone or a laptop. It needs no server
rendering or search indexing, and Node.js shouldn't have to run on the host.

## Decision

Build the UI with Svelte 5, SvelteKit, TypeScript, and Tailwind CSS as a single-page app: `ssr =
false` in the root layout and `adapter-static` with a `200.html` fallback. The Go binary embeds the
build (`internal/webui`) and serves `200.html` for every path that isn't a file, so the client-side
router handles deep links.

## Consequences

- Node.js is needed only to build. The `.deb` holds one binary with the UI inside.
- `npm run dev` runs Vite on its default port 5173 and proxies `/api` and `/healthz` to the Go
  server on 51821.
- SvelteKit's server code never runs in production, so advisories that affect only it (such as the
  low-severity `cookie` advisories reported on 2026-09-26) don't apply. CI's `npm audit` fails on
  moderate or higher.
- **Amended 2026-11-05: the audit covers runtime dependencies only.** Every package the web app
  ships is bundled by Vite, and the packages that remain in `node_modules` are build tools that
  never reach the host. By 2026-11-05 advisories in SvelteKit, `cookie`, `deepmerge`, and
  `minimatch` (through ESLint) had no fix short of a major upgrade, so the gate failed on every
  change, including ones that touched no web code. CI now runs `npm audit --omit=dev
  --audit-level=moderate`. The build tools are still updated by Dependabot, and an advisory in one
  is a reason to update it when a fix exists, not a reason to block unrelated changes.
- The strict Content-Security-Policy planned for M2 has to allow SvelteKit's inline bootstrap script
  by hash, since the fallback page contains one.
