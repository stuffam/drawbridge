# ADR 0013: A documentation site built with Zensical, on GitHub Pages

- **Status:** Accepted (2026-10-04)
- **Plan reference:** docs/PLAN.md §3, D13

## Context

The guides, and the install, router, and troubleshooting docs that are coming, are Markdown in
`docs/`. A reader who isn't a contributor needs more than a file browser: navigation, search, a page
that works on a phone, and a stable URL. The repository already has GitHub Pages, which served the
repository's root with Jekyll.

Material for MkDocs is the usual choice, but its authors moved it into maintenance mode on
2025-11-06 (critical fixes for at least 12 months, no new features) and made Zensical, a new static
site generator by the same team, its successor. A site built on Material today would have to move
within a year.

## Decision

Publish `docs/` as a static site built by Zensical (`zensical.toml`), by a GitHub Actions workflow
(`.github/workflows/docs.yml`), at https://stuffam.github.io/drawbridge/. The repository's Pages
source is "GitHub Actions", because a branch source can serve only the repository's root or `docs/`
as it is, and can't build anything. The site uses Zensical's `modern` design.

The docs stay GitHub-flavored Markdown that reads the same on GitHub and on the site. Zensical's
parser, Python-Markdown, reads two things differently from GitHub: a nested list has to be indented
4 spaces, not 2, and a list has to follow a blank line. A page that breaks either still builds, with
its bullets flattened or folded into the paragraph above. So `test/docs/check_lists.py` reads each
page the way GitHub does, compares every list item's depth with the built page's, and fails the
build when they differ. `zensical.toml` turns on only the Markdown extensions that don't
reinterpret GitHub-flavored text: Zensical's starter list also has math (`$...$`), subscripts
(`~x~`), and fractions (`1/2`).

## Consequences

- Python 3.10 or later is a tool for the docs only: nothing in the daemon, the web app, or the
  `.deb` uses it, and the product stays pure Go. `make docs` makes its own virtualenv.
- Zensical is alpha (its PyPI classifier says so, and its version is 0.0.x). `requirements-docs.txt`
  pins it and what it needs, Dependabot proposes updates, and the build is strict on every pull
  request that touches the docs, so a release that breaks a page or a link fails there. If it
  stalls, its `classic` design keeps Material's look, and the configuration translates to a
  `mkdocs.yml` for Material for MkDocs.
- A page's URL follows its file name (`/PLAN/`, `/MANUAL_CHECKLIST/`). Renaming a doc changes its
  URL, and the README, `CLAUDE.md`, and the code refer to the files by name.
- The navigation is a list in `zensical.toml`. A page that isn't listed is built but not linked.
- A change to `docs/` is published when it reaches `main`.
