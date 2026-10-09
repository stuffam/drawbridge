#!/usr/bin/env bash
# Tests the scripts that decide and check a release (docs/PLAN.md §11.1): changelog-release.sh,
# release-notes.sh, release-check.sh, docs-version.sh, and release-verify.sh. Each runs against a throwaway git repository and
# directory, so nothing here touches the checkout or the host. release-verify.sh needs dpkg-deb
# (Debian and Ubuntu have it; a laptop usually doesn't), and its cases are skipped without it.
# `make test-release` runs this.
set -uo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
notes=$root/scripts/release-notes.sh
release=$root/scripts/changelog-release.sh
check=$root/scripts/release-check.sh
docsver=$root/scripts/docs-version.sh
verify=$root/scripts/release-verify.sh
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

passed=0 failed=0
pass() { passed=$((passed + 1)); }
fail() {
	failed=$((failed + 1))
	echo "FAIL: $1" >&2
	[ -z "${2:-}" ] || printf '%s\n' "$2" | sed 's/^/      /' >&2
}

# expect NAME STATUS OUTPUT-REGEX COMMAND...: the command's exit status is STATUS (0 or nonzero)
# and its output (both streams) matches the regex.
expect() {
	local name=$1 want=$2 re=$3 out status
	shift 3
	out=$("$@" 2>&1)
	status=$?
	if [ "$want" = 0 ] && [ "$status" != 0 ]; then
		fail "$name: exited $status" "$out"
	elif [ "$want" != 0 ] && [ "$status" = 0 ]; then
		fail "$name: succeeded, but should have failed" "$out"
	elif ! printf '%s\n' "$out" | grep -Eq -- "$re"; then
		fail "$name: output doesn't match /$re/" "$out"
	else
		pass
	fi
}

# same NAME WANT GOT: passes when the two are equal, and shows both when they aren't.
same() {
	if [ "$2" = "$3" ]; then
		pass
	else
		fail "$1" "want: $2
got:  $3"
	fi
}

# ---- release-notes.sh ----

# Keep a Changelog's form: bracketed versions with an em dash and a date, `---` between releases,
# and link definitions at the foot. A version without brackets or a date still works.
cat >"$work/CHANGELOG.md" <<'EOF'
# Changelog

Preamble.

---

## [Unreleased]

### Added

- Not released yet.

---

## [v1.2.0] — 2026-12-01

Intro line.

### Notes

Keep this line, and [its guide][guide].

[guide]: https://example.com/guide

More text after the definition.

### Added

- A change.


---

## v1.1.0

The middle one.

## [v1.0.0-rc.1] - 2026-10-01

A pre-release.

## [v1.0.0]

## [v0.9.0] — 2026-09-01

Last.

---

[Unreleased]: https://example.com/compare/v1.2.0...main
[v1.2.0]: https://example.com/compare/v1.1.0...v1.2.0
[v0.9.0]: https://example.com/releases/v0.9.0
EOF

expect "notes: a bracketed, dated section, subheadings kept, next section left out" 0 '^### Notes$' \
	"$notes" v1.2.0 "$work/CHANGELOG.md"
out=$("$notes" v1.2.0 "$work/CHANGELOG.md")
same "notes: trimmed to the section's own text (no heading, no trailing ---), a mid-section link definition kept" \
	$'Intro line.\n\n### Notes\n\nKeep this line, and [its guide][guide].\n\n[guide]: https://example.com/guide\n\nMore text after the definition.\n\n### Added\n\n- A change.' "$out"
case $out in
*"Not released yet"*) fail "notes: [Unreleased]'s entries leaked into v1.2.0's notes" ;;
*) pass ;;
esac
expect "notes: a section without brackets or a date" 0 '^The middle one\.$' "$notes" v1.1.0 "$work/CHANGELOG.md"
expect "notes: v1.0.0 isn't v1.0.0-rc.1's section" 1 'empty one' "$notes" v1.0.0 "$work/CHANGELOG.md"
expect "notes: a pre-release's own section, dated with a hyphen" 0 '^A pre-release\.$' "$notes" v1.0.0-rc.1 "$work/CHANGELOG.md"
out=$("$notes" v0.9.0 "$work/CHANGELOG.md")
same "notes: the last section, without its --- or the link definitions at the foot of the file" 'Last.' "$out"
expect "notes: a version with no section" 1 'no section' "$notes" v3.0.0 "$work/CHANGELOG.md"
expect "notes: no changelog" 1 "can't read" "$notes" v1.0.0 "$work/nope.md"
expect "notes: a missing argument" 1 'usage' "$notes"

# ---- changelog-release.sh ----

cat >"$work/cl-start.md" <<'EOF'
# Changelog

Preamble.

---

## [Unreleased]

### Added

- **A feature.** It does a thing.

### Fixed

- **A bug.** It no longer happens.

---

## [v1.1.0] — 2026-10-02

### Added

- Older.

---

## [v1.0.0-rc.1] — 2026-10-01

First.

---

[Unreleased]: https://example.com/o/r/compare/v1.1.0...main
[v1.1.0]: https://example.com/o/r/compare/v1.0.0-rc.1...v1.1.0
[v1.0.0-rc.1]: https://example.com/o/r/releases/tag/v1.0.0-rc.1
EOF
cat >"$work/cl-want.md" <<'EOF'
# Changelog

Preamble.

---

## [Unreleased]

---

## [v1.2.0] — 2026-11-01

### Added

- **A feature.** It does a thing.

### Fixed

- **A bug.** It no longer happens.

---

## [v1.1.0] — 2026-10-02

### Added

- Older.

---

## [v1.0.0-rc.1] — 2026-10-01

First.

---

[Unreleased]: https://example.com/o/r/compare/v1.2.0...main
[v1.2.0]: https://example.com/o/r/compare/v1.1.0...v1.2.0
[v1.1.0]: https://example.com/o/r/compare/v1.0.0-rc.1...v1.1.0
[v1.0.0-rc.1]: https://example.com/o/r/releases/tag/v1.0.0-rc.1
EOF

# fresh NAME: a copy of the starting changelog to run the script on.
fresh() { cp "$work/cl-start.md" "$work/$1.md"; }

fresh cl
expect "changelog-release: says what it did" 0 'Turned \[Unreleased\] into \[v1.2.0\] — 2026-11-01' \
	"$release" v1.2.0 2026-11-01 "$work/cl.md"
same "changelog-release: [Unreleased] becomes the version, a fresh one above it, and every link is rebuilt" \
	"$(cat "$work/cl-want.md")" "$(cat "$work/cl.md")"
out=$("$notes" v1.2.0 "$work/cl.md")
same "changelog-release: release-notes.sh reads the new section: what was under [Unreleased]" \
	$'### Added\n\n- **A feature.** It does a thing.\n\n### Fixed\n\n- **A bug.** It no longer happens.' "$out"

fresh cl
REPO_URL=https://github.com/x/y/ "$release" v1.2.0 2026-11-01 "$work/cl.md" >/dev/null
expect "changelog-release: REPO_URL sets the links' address" 0 'https://github.com/x/y/compare/v1.2.0\.\.\.main' cat "$work/cl.md"
fresh cl
BASE_BRANCH=trunk "$release" v1.2.0 2026-11-01 "$work/cl.md" >/dev/null
expect "changelog-release: BASE_BRANCH sets what [Unreleased] compares with" 0 'compare/v1.2.0\.\.\.trunk' cat "$work/cl.md"

fresh cl
"$release" v2.0.0-rc.1 2026-11-01 "$work/cl.md" >/dev/null
expect "changelog-release: a pre-release is a version" 0 '^## \[v2.0.0-rc.1\] — 2026-11-01$' cat "$work/cl.md"
fresh cl
today=$(date -u +%Y-%m-%d)
"$release" v1.2.0 "" "$work/cl.md" >/dev/null
tomorrow=$(date -u +%Y-%m-%d) # in case the run crossed midnight UTC
expect "changelog-release: without a date it's today's, in UTC" 0 "^## \\[v1.2.0\\] — ($today|$tomorrow)\$" cat "$work/cl.md"

# The first release after a release candidate: the release's links compare with the candidate.
cat >"$work/cl-rc.md" <<'EOF'
# Changelog

---

## [Unreleased]

- **Something.** New.

---

## [v1.0.0-rc.1] — 2026-10-01

First.

---

[Unreleased]: https://example.com/o/r/compare/v1.0.0-rc.1...main
[v1.0.0-rc.1]: https://example.com/o/r/releases/tag/v1.0.0-rc.1
EOF
"$release" v1.0.0 2026-10-09 "$work/cl-rc.md" >/dev/null
expect "changelog-release: a release after its candidate compares with it" 0 '^\[v1.0.0\]: https://example.com/o/r/compare/v1.0.0-rc.1\.\.\.v1.0.0$' cat "$work/cl-rc.md"
expect "changelog-release: ...and the candidate, now the oldest, links to its release page" 0 '^\[v1.0.0-rc.1\]: https://example.com/o/r/releases/tag/v1.0.0-rc.1$' cat "$work/cl-rc.md"

# Refusals leave the file as it was.
refuses() { # NAME REGEX FILE ARGS...: the script fails with the message, and the file is unchanged
	local name=$1 re=$2 f=$3
	shift 3
	cp "$f" "$f.before"
	expect "changelog-release: $name" 1 "$re" "$release" "$@" "$f"
	if cmp -s "$f" "$f.before"; then pass; else fail "changelog-release: $name changed the file"; fi
}
printf '# Changelog\n\n## [Unreleased]\n\n### Added\n\n### Fixed\n\n---\n\n## [v1.0.0] — 2026-10-01\n\nOld.\n\n---\n\n[Unreleased]: https://example.com/o/r/compare/v1.0.0...main\n[v1.0.0]: https://example.com/o/r/releases/tag/v1.0.0\n' >"$work/cl-empty.md"
refuses "an [Unreleased] with only headings" 'has nothing under it' "$work/cl-empty.md" v1.1.0 2026-11-01
fresh cl
refuses "a version that already has a section" 'already has a section for v1.1.0' "$work/cl.md" v1.1.0 2026-11-01
refuses "a version that isn't one" "1.2 isn't a version" "$work/cl.md" 1.2 2026-11-01
refuses "a version without its v" "1.2.0 isn't a version" "$work/cl.md" 1.2.0 2026-11-01
refuses "a version with build metadata" "isn't a version" "$work/cl.md" v1.2.0+build 2026-11-01
refuses "a date that isn't one" 'YYYY-MM-DD, not 1 November' "$work/cl.md" v1.2.0 "1 November"
printf '# Changelog\n\n## [v1.0.0] — 2026-10-01\n\nOld.\n\n---\n\n[v1.0.0]: https://example.com/o/r/releases/tag/v1.0.0\n' >"$work/cl-none.md"
REPO_URL=https://example.com/o/r refuses "no [Unreleased] section" 'no \[Unreleased\] section' "$work/cl-none.md" v1.1.0 2026-11-01
printf '# Changelog\n\n## [Unreleased]\n\n- Something.\n' >"$work/cl-nolinks.md"
refuses "no address for the links" 'no REPO_URL' "$work/cl-nolinks.md" v1.0.0 2026-11-01
expect "changelog-release: no changelog" 1 "can't read" "$release" v1.2.0 2026-11-01 "$work/nope.md"
expect "changelog-release: a missing argument" 1 'usage' "$release"

# ---- release-check.sh ----

repo=$work/repo
mkdir "$repo"
git() { command git -C "$repo" -c user.name=t -c user.email=t@example.com -c commit.gpgsign=false -c tag.gpgsign=false "$@"; }
git init -q -b main
cp "$work/CHANGELOG.md" "$repo/CHANGELOG.md"
git add CHANGELOG.md
git commit -q -m "changelog"
git update-ref refs/remotes/origin/main HEAD
git tag v1.2.0
git tag v1.0.0-rc.1
git tag v0.9.0
git tag v1.1.0
# A commit that isn't on main, with a tag on it, and one that has no changelog section.
git checkout -q -b side
git commit -q --allow-empty -m "unmerged"
git tag v1.2.1
git checkout -q main
printf '\n## [v5.0.0] — 2026-10-07\n\nA new one.\n' >>"$repo/CHANGELOG.md"
git commit -q -am "v5.0.0's section"
git update-ref refs/remotes/origin/main HEAD
git tag v5.0.0
git tag v7.0.0 # has no section

in_repo() { (cd "$repo" && "$@"); }
export CHANGELOG=$repo/CHANGELOG.md

expect "check: a release on main with a section" 0 '^tag=v1.2.0$' in_repo "$check" v1.2.0
same "check: outputs for v1.2.0" $'tag=v1.2.0\nversion=1.2.0\ndeb_version=1.2.0\nprerelease=false' \
	"$(in_repo "$check" v1.2.0)"
same "check: outputs for v1.0.0-rc.1" $'tag=v1.0.0-rc.1\nversion=1.0.0-rc.1\ndeb_version=1.0.0~rc.1\nprerelease=true' \
	"$(in_repo "$check" v1.0.0-rc.1)"
expect "check: v0.9.0 (before 1.0)" 0 '^prerelease=false$' in_repo "$check" v0.9.0

for bad in v1 1.2.0 v1.2 v01.2.0 v1.2.0+build v1.2.0- v1.2.0-rc..1 v1.2.0-rc_1 vv1.2.0 "v1.2.0 " ""; do
	expect "check: '$bad' isn't a version" 1 "isn't a version" in_repo "$check" "$bad"
done
expect "check: no such tag" 1 "no tag v2.0.0" in_repo "$check" v2.0.0
expect "check: a tag that isn't on main" 1 "isn't on main" in_repo "$check" v1.2.1
expect "check: no changelog section" 1 "needs a section for v7.0.0" in_repo "$check" v7.0.0
expect "check: a tag's own section is enough" 0 '^version=5.0.0$' in_repo "$check" v5.0.0
git update-ref -d refs/remotes/origin/main
expect "check: no origin/main in the clone" 1 "can't find origin/main" in_repo "$check" v1.2.0
expect "check: MAIN_REF names another ref" 0 '^tag=v1.2.0$' env MAIN_REF=main bash -c "cd '$repo' && '$check' v1.2.0"
unset CHANGELOG

# ---- docs-version.sh ----

drepo=$work/docs-repo
mkdir "$drepo"
dgit() { command git -C "$drepo" -c user.name=t -c user.email=t@example.com -c commit.gpgsign=false -c tag.gpgsign=false "$@"; }
dgit init -q -b main
dgit commit -q --allow-empty -m "start"
for t in v0.9.0 v1.2.0 v1.10.0 v1.0.0-rc.1 v2.0.0-rc.1; do dgit tag "$t"; done
in_docs_repo() { (cd "$drepo" && "$@"); }

# The docs version of a release is its minor, and plain mode never reads git.
same "docs-version: v1.2.0 is v1.2" "v1.2" "$(env GIT_DIR=/nonexistent "$docsver" v1.2.0)"
same "docs-version: v0.9.0 is v0.9" "v0.9" "$(env GIT_DIR=/nonexistent "$docsver" v0.9.0)"
same "docs-version: v1.10.0 is v1.10" "v1.10" "$(env GIT_DIR=/nonexistent "$docsver" v1.10.0)"

# Only a final vMAJOR.MINOR.PATCH publishes: a pre-release, a build suffix, a path, shell syntax, and
# a newline never reach mike.
for bad in v1.0.0-rc.1 v1.2.0-rc.1 1.2.0 v1.2 v1 v01.2.0 v1.2.0+build v1.2.0- vv1.2.0 v1.2.0/../x \
	'v1.2.0;id' 'v1.2.0 ' '' $'v1.2.0\nx'; do
	expect "docs-version: '$bad' isn't a release version" 1 "isn't a release version" "$docsver" "$bad"
done
expect "docs-version: no arguments" 1 "usage" "$docsver"
expect "docs-version: three arguments" 1 "usage" "$docsver" --latest v1.2.0 extra
"$docsver" >/dev/null 2>&1
same "docs-version: no arguments exits 2" 2 "$?"
"$docsver" --latest v1.2.0 extra >/dev/null 2>&1
same "docs-version: three arguments exit 2" 2 "$?"

# --latest: the highest final tag wins, by version and not by text, and a pre-release doesn't count.
expect "docs-version: --latest v1.10.0 beats v1.2.0" 0 '^$' in_docs_repo "$docsver" --latest v1.10.0
expect "docs-version: --latest v1.2.0 loses to v1.10.0" 1 '^$' in_docs_repo "$docsver" --latest v1.2.0
expect "docs-version: --latest v0.9.0 loses" 1 '^$' in_docs_repo "$docsver" --latest v0.9.0
expect "docs-version: --latest of a pre-release" 1 "isn't a release version" in_docs_repo "$docsver" --latest v2.0.0-rc.1
expect "docs-version: --latest of shell syntax" 1 "isn't a release version" in_docs_repo "$docsver" --latest 'v1.2.0;id'
expect "docs-version: --latest of a tag that isn't there" 1 "no tag v3.0.0" in_docs_repo "$docsver" --latest v3.0.0
dgit tag v2.0.0
expect "docs-version: --latest v2.0.0 once it exists" 0 '^$' in_docs_repo "$docsver" --latest v2.0.0
expect "docs-version: --latest v1.10.0 loses to v2.0.0" 1 '^$' in_docs_repo "$docsver" --latest v1.10.0

# ---- release-verify.sh ----

if ! command -v dpkg-deb >/dev/null 2>&1; then
	echo "SKIP: release-verify.sh's cases (no dpkg-deb)"
else
	# A release's files are named for the version, and a package's own Version is the Debian form.
	ver=1.0.0-rc.1
	debver=1.0.0~rc.1
	# A package with the given version and architecture, and the dependencies the real one has
	# unless a case gives others (an empty one leaves the field out).
	make_deb() {
		local out=$1 v=$2 arch=$3 tree
		local depends=${4-nftables} recommends=${5-wireguard-tools, systemd-timesyncd | time-daemon}
		tree=$(mktemp -d "$work/deb.XXXXXX")
		mkdir -p "$tree/DEBIAN"
		printf 'Package: drawbridge\nVersion: %s\nArchitecture: %s\nMaintainer: t <t@example.com>\nDescription: test\n' \
			"$v" "$arch" >"$tree/DEBIAN/control"
		[ -z "$depends" ] || printf 'Depends: %s\n' "$depends" >>"$tree/DEBIAN/control"
		[ -z "$recommends" ] || printf 'Recommends: %s\n' "$recommends" >>"$tree/DEBIAN/control"
		dpkg-deb --build "$tree" "$out" >/dev/null 2>&1
	}
	# A directory holding a release, which a case then spoils in one way.
	make_release() {
		local d=$1
		mkdir -p "$d"
		for arch in arm64 amd64; do
			make_deb "$d/drawbridge_${ver}_$arch.deb" "$debver" "$arch"
			echo '{"bomFormat":"CycloneDX"}' >"$d/drawbridge_${ver}_$arch.sbom.json"
		done
		echo '{"bomFormat":"CycloneDX"}' >"$d/drawbridge-web_$ver.sbom.json"
		printf '#!/bin/sh\n' >"$d/install.sh"
		chmod +x "$d/install.sh"
		resum "$d"
	}
	# SHA256SUMS for every file in the directory but itself, in the order a release lists them.
	resum() {
		(
			cd "$1" || exit 1
			for f in *; do [ "$f" = SHA256SUMS ] || echo "$f"; done | LC_ALL=C sort | xargs sha256sum >"$work/sums"
			mv "$work/sums" SHA256SUMS
		)
	}

	make_release "$work/ok"
	expect "verify: a whole release" 0 'release files OK: drawbridge 1.0.0-rc.1' "$verify" "$work/ok" "$ver"
	expect "verify: the wrong version" 1 "doesn't hold exactly" "$verify" "$work/ok" 1.0.0
	# GitHub renames a ~ in a file's name to a ., so a release's files can't be named for the Debian version.
	expect "verify: files must be named for the version, not the Debian version" 1 "doesn't hold exactly" "$verify" "$work/ok" "$debver"
	expect "verify: a usage error" 2 'usage' "$verify"

	make_release "$work/extra"
	echo x >"$work/extra/notes.txt"
	expect "verify: an extra file" 1 "doesn't hold exactly" "$verify" "$work/extra" "$ver"

	make_release "$work/missing"
	rm "$work/missing/drawbridge-web_$ver.sbom.json"
	expect "verify: a missing SBOM" 1 "doesn't hold exactly" "$verify" "$work/missing" "$ver"

	make_release "$work/changed"
	echo '{"bomFormat":"CycloneDX","x":1}' >"$work/changed/drawbridge_${ver}_arm64.sbom.json"
	expect "verify: a file changed after its checksum" 1 "doesn't match the files" "$verify" "$work/changed" "$ver"

	make_release "$work/unlisted"
	sed -i.bak '/install.sh/d' "$work/unlisted/SHA256SUMS" && rm "$work/unlisted/SHA256SUMS.bak"
	expect "verify: a file the checksums don't list" 1 "doesn't list exactly" "$verify" "$work/unlisted" "$ver"

	make_release "$work/version"
	make_deb "$work/version/drawbridge_${ver}_amd64.deb" 1.0.0 amd64
	resum "$work/version"
	expect "verify: a package of another version" 1 'has Version 1.0.0, not' "$verify" "$work/version" "$ver"

	make_release "$work/dash"
	make_deb "$work/dash/drawbridge_${ver}_amd64.deb" "$ver" amd64
	resum "$work/dash"
	expect "verify: a package whose Version has a - where the pre-release's ~ goes" 1 'has Version 1.0.0-rc.1, not 1.0.0~rc.1' "$verify" "$work/dash" "$ver"

	make_release "$work/arch"
	make_deb "$work/arch/drawbridge_${ver}_amd64.deb" "$debver" arm64
	resum "$work/arch"
	expect "verify: a package of another architecture" 1 'has Architecture arm64, not amd64' "$verify" "$work/arch" "$ver"

	make_release "$work/nodep"
	make_deb "$work/nodep/drawbridge_${ver}_amd64.deb" "$debver" amd64 "" "wireguard-tools, systemd-timesyncd | time-daemon"
	resum "$work/nodep"
	expect "verify: a package that doesn't depend on nftables" 1 "doesn't depend on nftables" "$verify" "$work/nodep" "$ver"

	make_release "$work/othernft"
	make_deb "$work/othernft/drawbridge_${ver}_arm64.deb" "$debver" arm64 "libnftables1" "wireguard-tools, systemd-timesyncd | time-daemon"
	resum "$work/othernft"
	expect "verify: a package that depends on something merely like nftables" 1 "doesn't depend on nftables" "$verify" "$work/othernft" "$ver"

	make_release "$work/notime"
	make_deb "$work/notime/drawbridge_${ver}_amd64.deb" "$debver" amd64 nftables "wireguard-tools"
	resum "$work/notime"
	expect "verify: a package that doesn't recommend a time daemon" 1 "doesn't recommend systemd-timesyncd" "$verify" "$work/notime" "$ver"

	make_release "$work/nowg"
	make_deb "$work/nowg/drawbridge_${ver}_amd64.deb" "$debver" amd64 nftables "systemd-timesyncd | time-daemon"
	resum "$work/nowg"
	expect "verify: a package that doesn't recommend wireguard-tools" 1 "doesn't recommend wireguard-tools" "$verify" "$work/nowg" "$ver"

	make_release "$work/alsodeps"
	make_deb "$work/alsodeps/drawbridge_${ver}_amd64.deb" "$debver" amd64 "libc6 (>= 2.31), nftables (>= 1.0), adduser" "wireguard-tools, systemd-timesyncd | time-daemon"
	resum "$work/alsodeps"
	expect "verify: nftables among other dependencies, with a version" 0 'release files OK' "$verify" "$work/alsodeps" "$ver"

	make_release "$work/sbom"
	echo 'not json' >"$work/sbom/drawbridge_${ver}_amd64.sbom.json"
	resum "$work/sbom"
	expect "verify: an SBOM that isn't one" 1 "isn't a CycloneDX document" "$verify" "$work/sbom" "$ver"

	make_release "$work/exec"
	chmod -x "$work/exec/install.sh"
	expect "verify: install.sh not executable" 1 "isn't executable" "$verify" "$work/exec" "$ver"
fi

echo "$passed passed, $failed failed"
[ "$failed" = 0 ]
