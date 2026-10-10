# Releasing Drawbridge

This is the maintainer's guide to cutting a release. You do three things by hand: write what
changed, push a tag, and, once you've tried the release's own files on real hardware, publish it.
CI does the rest. [PLAN.md §11.1](PLAN.md#111-releases-tag-build-publish-install) says why it's
built this way.

| Step | Who | What |
| --- | --- | --- |
| 1. Choose the version | you | `v0.1.0`, `v0.1.1`, `v0.2.0-rc.1` |
| 2. Write the changelog | you, and the **Prepare release** workflow | `[Unreleased]` becomes the version's section, in a pull request you merge |
| 3. Tag it | you | `git tag -a`, a check, `git push` |
| 4. Build and draft | CI | checks the tag, runs every CI job on it, builds both packages twice, makes build provenance, and drafts the release |
| 5. Try the draft's files | you | on the Pi: fresh, and as an upgrade with a client connected |
| 6. Publish | you | one command, or one button |
| 7. Afterward | you | a few lines of bookkeeping |

## Before your first release

You do these once.

| Check | Where | Why |
| --- | --- | --- |
| A tag ruleset on `v*` blocks updates, deletions, and non-fast-forward pushes, with no bypass actors | **Settings → Rules → Rulesets** | A tag can never move under someone who installed from it. It also means **a tag you push by mistake stays**: step 3 shows how to check first. |
| Immutable releases are on | **Settings → General → Releases** | A published release's files can't be replaced. |
| `gh`, the GitHub command line, is installed and logged in, at version 2.49 or later | `gh --version`, `gh auth status` | A draft's files need your login, and it's the easiest way to publish. `gh attestation` (step 5) arrived in 2.49, and a `gh` from a distribution's packages is often older. If the Pi's is, install GitHub's own, or do that one check on a machine with a current `gh`. |
| A host to try the files on | the reference platform (a Raspberry Pi 5 on Debian 13) | Step 5. The checklist in [MANUAL_CHECKLIST.md](MANUAL_CHECKLIST.md) says what to look at. |
| GitHub Pages serves the `gh-pages` branch | **Settings → Pages → Build and deployment**: source **Deploy from a branch**, branch `gh-pages`, folder `/ (root)`. Then **Settings → Environments → github-pages → Deployment branches and tags**: allow `gh-pages` | The docs site is versioned with mike, which commits each version to that branch ([Docs versions](#docs-versions)). The branch doesn't exist until the **Docs** workflow first publishes `dev`, from a push to `main` that touches the docs or a manual run on `main`, so switch the source after that. The environment is the second step because the "GitHub Actions" source left it allowing only `main`, and GitHub's own deployment of a branch goes through that environment, so `gh-pages` has to be allowed in it or the deployment should be refused. Done on 2026-10-09: with both set, the branch deployed. The refusal itself wasn't seen, because both were changed together. |

## Step 1: choose the version

A version is `vMAJOR.MINOR.PATCH`, with a lowercase `v`. A suffix, as in `v0.2.0-rc.1`, makes it a
pre-release: public, but never GitHub's "latest", so `install.sh` skips it unless it's asked for
with `--version`.

| Change | Example |
| --- | --- |
| Bug fixes only | `v0.1.0` → `v0.1.1` |
| New features, nothing broken | `v0.1.0` → `v0.2.0` |
| A release candidate, to try the files first | `v0.2.0-rc.1` |

The tag has to look like that. Release workflows start only for a tag that begins with `v`, and
the first job refuses one that isn't a version.

## Step 2: write the changelog

[CHANGELOG.md](https://github.com/stuffam/drawbridge/blob/main/CHANGELOG.md) is in Keep a
Changelog's form, and a release's notes on GitHub are its section. Changes go under
`## [Unreleased]` as they're made. The section for a release leads with what an admin must know
before installing or upgrading (a downgrade isn't supported, a setting that now means something
else, a test that was changed on purpose), then what changed, under Added, Changed, Fixed, and the
like.

A published release's notes can't change, so a link in them has to stay right. Link to a guide on
the docs site by its version, `https://stuffam.github.io/drawbridge/v0.1/getting-started/`, where
`v0.1` is the release's minor. Link to a file that isn't on the site (the requirements, the manual
checklist, PLAN) by the tag, `https://github.com/stuffam/drawbridge/blob/v0.1.0/docs/PLAN.md`, which
works once the tag is pushed. A site address with no version in front, such as `/install/`, doesn't
work, and neither does `/latest/...` before the first final release. The footer that **Draft the
release** adds does this for the install guide itself (`scripts/release-install-url.sh`), and a test
fails if the changelog, the workflows, or the scripts link to a site address without a version.

To turn `[Unreleased]` into the release:

1. On GitHub, open **Actions → Prepare release → Run workflow**, on `main`.
2. Type the version (`v0.1.0`) and, if you don't want today's date (UTC), the date.
3. When it finishes, open its run and use the link in the summary: it opens a pull request from
   the branch `release/v0.1.0`, with a title and description already filled in.
4. Read the section the pull request makes. It becomes the release's notes. Fix the wording in the
   pull request if you need to, wait for CI, and merge it.

The workflow won't run, and says why, if the version isn't one, the changelog already has it, or
`[Unreleased]` has nothing under it. It stops at a branch because it can't open the pull request
itself: the setting that lets Actions open pull requests is off, and a pull request opened by
Actions wouldn't start the CI that `main` requires.

To do the same on your own machine:

```bash
git switch -c release/v0.1.0
sh scripts/changelog-release.sh v0.1.0
git commit -am "Add the changelog for v0.1.0"
```

## Step 3: tag it

Tag the commit on `main` that the changelog pull request made. A tag can't be deleted or moved
once it's pushed, so make it, check it, and only then push it:

```bash
git fetch origin
git switch main
git pull --ff-only
git tag -a v0.1.0 -m "Drawbridge v0.1.0"
scripts/release-check.sh v0.1.0
```

`release-check.sh` is the first job of the release workflow, run here instead. It prints the
version it found and exits 0, or says what's wrong: the tag isn't a version, its commit isn't on
`origin/main`, or the changelog has no section for it. If it complains, delete the tag you just
made (`git tag -d v0.1.0`), fix the cause, and try again. When it passes:

```bash
git push origin v0.1.0
```

GitHub's web page can't make a tag by itself: its release form creates the tag when it publishes
the release, and with immutable releases that would publish an empty release and stop the
workflow from making the real one. So the tag is pushed from a terminal.

## Step 4: watch the build

Open **Actions → Release** and the newest run, which is named for your tag. It takes about 15
minutes.

| Job | What it does |
| --- | --- |
| Check the tag | Refuses unless the tag is a version, on `main`, with a changelog section |
| CI | Every job of CI, on the tagged commit: the lint, the tests, the kernel tests, the package scripts |
| Build | The web app, both packages, their SBOMs, `install.sh`, and `SHA256SUMS`, checked against the tag |
| Build again | The packages again, on another machine. They have to match the first build byte for byte |
| Draft the release | Build provenance for every file, then a **draft** release with the files and the changelog section as its notes, and a check that GitHub kept every file's name |

When it's done, the run's summary says **The draft for the tag is ready**. Nothing is public yet.

## Step 5: try the draft's own files

A draft's files need your login, so a script can't try them; this is what you do by hand. Use the
draft's files, not a build from your tree: they're what users will get.

```bash
mkdir /tmp/release && cd /tmp/release
gh release download v0.1.0 --repo stuffam/drawbridge
sha256sum -c --ignore-missing SHA256SUMS
gh attestation verify drawbridge_*_arm64.deb --repo stuffam/drawbridge
```

`gh attestation verify` needs `gh` 2.49 or later. If the machine that downloads the files has an
older one (it answers `unknown command "attestation"`), run that one line on a machine with a
current `gh`, for the `.deb` and for `SHA256SUMS`. The Pi's `sha256sum -c` then shows that its copy
is the file whose provenance you checked.

Then, on the Pi, install the `.deb` the way [the install guide](user_docs/getting-started.md) says:
fresh, and as an upgrade over the previous release with a client connected. `drawbridge version`
should say the tag's version. The release's own check list is
[MANUAL_CHECKLIST.md](MANUAL_CHECKLIST.md) (§20 for what only a real tag shows, and the sections for
whatever changed since the last release); record what you saw there.

## Step 6: publish

```bash
gh release edit v0.1.0 --repo stuffam/drawbridge --draft=false
```

Or open the draft on GitHub and press **Publish release**. After this its files can't be changed
and its tag can't move, so don't publish until step 5 is done. A pre-release is published the
same way and is marked as one.

## Step 7: afterward

- Add the tag to `test/integration/upgrade-from.txt`, in its own pull request, so the upgrade
  tests start from this release too.
- After the first release candidate, try the path users take: on a host without Drawbridge,
  `sh install.sh --version v0.1.0-rc.2`. After `v0.1.0`, the same without `--version` should
  pick `v0.1.0` and not the candidate.
- After the first release, change what still says there's no release: the status text in
  CLAUDE.md and PLAN.md. The Getting Started page already sends readers to the latest release's
  package, so check that the
  [latest release](https://github.com/stuffam/drawbridge/releases/latest) is `v0.1.0`.
- After a final release, open https://stuffam.github.io/drawbridge/ and its version selector. The
  selector should list the release's minor, and when this has the highest version, mark it
  `latest` and send the root to it. If it doesn't, see [Docs versions](#docs-versions).

## Docs versions

The docs site keeps a version for each released minor, and `dev`. It needs nothing from you and
nothing from the changelog:

- **Publishing a final release** (step 6) starts the **Docs** workflow on the tag. It builds the
  docs and publishes them as the release's minor, `v0.1` for `v0.1.0` and `v0.1.1` alike, and moves
  `latest` to it when its version is the highest. The site's root goes to `latest`.
- **Publishing a pre-release** publishes nothing.
- **A push to `main` that touches the docs** publishes `dev`, and so does running the workflow by
  hand on `main`. The root goes to `dev` only until the first release.
- A patch for an older minor (`v0.1.5` after `v0.2.0`) refreshes `v0.1` and leaves `latest` on
  `v0.2`.
- "Highest" goes by the tags, not by which releases are published. A final tag with no published
  release (a draft, or a tag you gave up on) still counts, so a release below it doesn't move
  `latest`. If that leaves `latest` somewhere it shouldn't be, use the first command in the table
  below.
- It starts only when the release is published with your own login, the `gh` command or the button
  in step 6. GitHub doesn't start a workflow for an event that a workflow's own token caused, and
  nothing here publishes a release that way.

`scripts/docs-version.sh` decides the version and whether `latest` moves, and ADR 0013 has the
reasons.

Deleting a release, or marking it a pre-release, doesn't take its docs down. To fix a mistake, work
from a clone with the docs tools installed (`pip install -r requirements-docs.txt`, which needs
Python 3.10 or later) and a login that can push to the repository, and use mike. Each command takes
`--push` to publish. An alias is a redirect page here, so `mike alias` needs `--alias-type
redirect`, or it makes a symlink.

Run `git fetch origin` first, every time. mike never fetches: it reads the clone's own `gh-pages`
branch, or `origin/gh-pages` as of the last fetch when there's no local one, so in an old clone
`mike list` shows old versions, and a command with `--push` is refused as not a fast-forward. The
refused attempt still leaves its commit on the local `gh-pages`, and mike then warns that it has
diverged from `origin/gh-pages`. To get back, delete the local branch (`git branch -D gh-pages`,
which throws away only commits that were never pushed), run `git fetch origin`, and repeat the
command. After a fetch, a local `gh-pages` that is only behind is brought forward by mike itself.

| What happened | Command |
| --- | --- |
| `latest` is on the wrong version | `mike alias --update-aliases --alias-type redirect --push v0.2 latest` |
| The site's root goes somewhere else | `mike set-default --push latest` |
| A version shouldn't be there | `mike delete --push v0.1` (move `latest` off it first, or the root has nowhere to go) |
| You want to see what's published | `mike list` |

## If something goes wrong

A tag and a published release can't be changed, so a mistake is fixed by the next version.

| What happened | What to do |
| --- | --- |
| **Prepare release** failed: the version isn't one, the changelog has it already, or `[Unreleased]` is empty | Read the message in its log, fix the cause, and run it again |
| **Prepare release** failed at the push: the branch `release/<version>` exists | Delete that branch (it's an ordinary branch), or merge it if it's the one you want, and run it again |
| `release-check.sh` fails before you push | Nothing is public: delete the local tag, fix it, tag again |
| **Check the tag** fails after you pushed | The tag stays. Fix `main`, and release the next version (`v0.1.1`, or `rc.2`) |
| **CI** fails on the tag | If it's a flaky job, **Re-run failed jobs** on the run, which re-runs the same commit. If it's a real failure, fix `main` and release the next version |
| **Build again** says the packages differ | Don't publish. A change made a build depend on the time or the machine: find it (docs/PLAN.md §11.1 lists the three that did), fix it, and release the next version |
| **Draft the release** says GitHub renamed a file | Delete the draft (a draft can be deleted), and release the next version. GitHub rewrites a `~` in a file's name to a `.`, which is why a release's files are named for the version (`0.1.0-rc.2`), never the Debian version (`0.1.0~rc.2`): if the names changed in the build, that's where to look |
| You published, and found a problem | Delete the release or mark it a pre-release, so "latest" goes back to the last good one, say why in a note, and release the next version. Its docs stay up until you `mike delete` them ([Docs versions](#docs-versions)) |
| The **Docs** workflow's **Publish** job failed | Read its log. A build or a push that failed leaves the `gh-pages` branch as it was. A push is refused when another publish (a release's and a push to `main` have separate queues, so they can overlap) pushed first, and the log says it wasn't a fast-forward. Use **Re-run failed jobs**: both of a release's steps are safe to repeat, so a failure between them (the version published, the root not moved) is mended by the re-run |
| A release you published isn't on the docs site | Open **Actions → Docs** and look for a run for the release. There's none if the release was published by a workflow's token, or was a pre-release. A manual run publishes only `dev`, so for a release, check out its tag and run the commands of the **Publish the release's version** step in `.github/workflows/docs.yml` by hand, with `$TAG` and `$version` filled in |

## What isn't automated, and why

- **Publishing.** A package can't be taken back once a migration has run on a host that installed
  it, because an older build won't write the newer database. Trying the draft's files first is
  what keeps a bad release rare.
- **Stamping the version into the repository.** There's nothing to stamp: the version comes from
  the tag, and the package, the binary, and the OpenAPI document are built from it.
- **Committing to `main` from CI.** `main` requires a pull request and passing checks, with no
  exceptions. That's why the changelog is a pull request, and why the bookkeeping in step 7 is by
  hand.

## Trying the machinery without a release

Open **Actions → Release → Run workflow** on a branch, or open a pull request that touches the
release workflow, the packaging, `scripts/`, or the Makefile. Either runs a dry run: it builds and
checks the files and keeps them as a workflow artifact, without making a draft. `make test-release`
and `make test-install` test the scripts on your machine.
