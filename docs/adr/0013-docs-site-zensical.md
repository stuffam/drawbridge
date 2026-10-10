# ADR 0013: A documentation site built with Zensical, on GitHub Pages

- **Status:** Accepted (2026-10-04); amended 2026-10-09
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
- A page's URL follows its file name (`/getting-started/`, with a version in front, as amended
  below). Renaming a page changes its URL, and the README and the web UI link to pages by address.
- The navigation is a list in `zensical.toml`. A page that isn't listed is built but not linked.
- A change to `docs/` is published when it reaches `main`.
- **Amended 2026-10-09: versioned docs.** A reader who installed v0.1 shouldn't be reading what
  `main` says about a feature v0.1 doesn't have, so the site now keeps several versions, each in a
  directory of its own, managed by mike (Zensical's fork, `2.2.0+zensical-0.1.0`). The decision
  stands (Zensical, built from the repository, published to GitHub Pages by a workflow), but three
  things above no longer hold:
    - **The Pages source is the `gh-pages` branch, not "GitHub Actions".** mike keeps each
      version's built site in that branch, so the workflow commits there and Pages serves it. The
      objection above, that a branch source can't build anything, doesn't apply: nothing is built
      by Pages. The strict build and the list check still run first and gate every publish
      (`docs.yml`'s `build` job), and only its `publish` job can push, to that branch.
    - **A change that reaches `main` is published as `dev`, not as the site.** Each final release
      publishes its minor, `v0.1` for v0.1.0 and v0.1.1 alike, when the release is published (not
      when its tag is pushed, which comes first, before its files are tried on hardware), and
      moves the `latest` alias to it when its version is the highest. The site's root goes to
      `latest`, or to `dev` until the first release. A pre-release publishes nothing, and a patch
      for an older minor leaves `latest` where it is. `scripts/docs-version.sh` decides a
      release's version and whether `latest` moves, and has tests; the workflow decides the rest.
    - **A page's URL has a version in front of it** (`/latest/getting-started/`). A link to an old
      address without one, such as `/getting-started/`, no longer lands anywhere; only the root
      redirects.

    The fork is transitional, kept by Zensical's authors until Zensical has versioning of its own,
    so it is pinned to a tag in `requirements-docs.txt` and updated by hand. An alias is a small
    redirect page, not a symlink, because whether GitHub Pages serves a symlink wasn't observed.
    `latest` goes by the release tags and not by which releases are published, so a final tag with
    no published release still counts. A release's publish has a concurrency group of its own,
    because GitHub keeps one run waiting in a group and a push to `main` would replace a release's
    waiting publish; the two can then overlap, and whichever pushes second is refused (mike never
    forces a push) and is run again. The publish commands were run against a local remote on
    2026-10-09, through a release history (v0.1.0, v0.2.0, a v0.1.1 patch, a push to `main` after a
    release, a pre-release, and a hostile tag) with the results as intended. The first run on
    GitHub, for the merge of this change into `main` the same day, then succeeded: it made the
    `gh-pages` branch with `dev` in it, and the branch's root redirects there. That evening the
    maintainer switched the Pages source to the branch and allowed `gh-pages` in the `github-pages`
    environment, which until then allowed only `main`, and GitHub served the site: the Pages build
    was clean, `/dev/` and `versions.json` answered, and the page carried the selector's settings.
    Not yet observed: what the environment does without that second setting (both were changed
    together), and a release's publish.
- **Amended 2026-10-09: the user docs are written for the site.** The guides moved to
  `docs/user_docs/`, and most pages there start with front matter (`title:`, and `hide:` on the
  home and getting-started pages) instead of a `# Title`. The site shows the title, because the
  theme adds one to a page that has none, but GitHub shows such a file as a table of those fields
  with no heading, so the claim above that a page reads the same on GitHub and on the site no
  longer holds for these pages. The maintainer accepted that, because the site is where they are
  read. What stays is the rule about syntax: the Markdown is still GitHub-flavored, the extension
  list in `zensical.toml` is still the short one that doesn't reinterpret it, and
  `test/docs/check_lists.py` still compares every list with GitHub's reading, allowing for the
  front matter and for the title the theme adds. Documents outside `docs/user_docs/`, such as
  PLAN.md and the ADRs, are read on GitHub and keep their `# Title`. They aren't published: the
  site is `docs/user_docs/` alone (`docs_dir` in `zensical.toml`), for the end user, and a page
  there covers one topic and links to no file outside it. The rest of `docs/` is for contributors.
