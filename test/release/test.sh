#!/usr/bin/env bash
# Tests the scripts that decide and check a release (docs/PLAN.md §11.1): release-notes.sh,
# release-check.sh, and release-verify.sh. Each runs against a throwaway git repository and
# directory, so nothing here touches the checkout or the host. release-verify.sh needs dpkg-deb
# (Debian and Ubuntu have it; a laptop usually doesn't), and its cases are skipped without it.
# `make test-release` runs this.
set -uo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
notes=$root/scripts/release-notes.sh
check=$root/scripts/release-check.sh
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

cat >"$work/CHANGELOG.md" <<'EOF'
# Changelog

Preamble.

## v1.2.0 - 2026-12-01

### Upgrading

Keep this line.

### Changes

- A change.


## v1.1.0

The middle one.

## v1.0.0-rc.1 - 2026-10-01

A pre-release.

## v1.0.0 - 2026-10-02

## v0.9.0

Last.
EOF

expect "notes: a dated section, subheadings kept, next section left out" 0 '^### Upgrading$' \
	"$notes" v1.2.0 "$work/CHANGELOG.md"
out=$("$notes" v1.2.0 "$work/CHANGELOG.md")
same "notes: trimmed to the section's own text" $'### Upgrading\n\nKeep this line.\n\n### Changes\n\n- A change.' "$out"
expect "notes: a section without a date" 0 '^The middle one\.$' "$notes" v1.1.0 "$work/CHANGELOG.md"
expect "notes: v1.0.0 isn't v1.0.0-rc.1's section" 1 'empty one' "$notes" v1.0.0 "$work/CHANGELOG.md"
expect "notes: a pre-release's own section" 0 '^A pre-release\.$' "$notes" v1.0.0-rc.1 "$work/CHANGELOG.md"
expect "notes: the last section" 0 '^Last\.$' "$notes" v0.9.0 "$work/CHANGELOG.md"
expect "notes: a version with no section" 1 'no section' "$notes" v3.0.0 "$work/CHANGELOG.md"
expect "notes: no changelog" 1 "can't read" "$notes" v1.0.0 "$work/nope.md"
expect "notes: a missing argument" 1 'usage' "$notes"

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
printf '\n## v5.0.0\n\nA new one.\n' >>"$repo/CHANGELOG.md"
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

# ---- release-verify.sh ----

if ! command -v dpkg-deb >/dev/null 2>&1; then
	echo "SKIP: release-verify.sh's cases (no dpkg-deb)"
else
	ver=1.0.0~rc.1
	# A package with the given version and architecture.
	make_deb() {
		local out=$1 v=$2 arch=$3 tree
		tree=$(mktemp -d "$work/deb.XXXXXX")
		mkdir -p "$tree/DEBIAN"
		printf 'Package: drawbridge\nVersion: %s\nArchitecture: %s\nMaintainer: t <t@example.com>\nDescription: test\n' \
			"$v" "$arch" >"$tree/DEBIAN/control"
		dpkg-deb --build "$tree" "$out" >/dev/null 2>&1
	}
	# A directory holding a release, which a case then spoils in one way.
	make_release() {
		local d=$1
		mkdir -p "$d"
		for arch in arm64 amd64; do
			make_deb "$d/drawbridge_${ver}_$arch.deb" "$ver" "$arch"
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
	expect "verify: a whole release" 0 'release files OK: drawbridge 1.0.0~rc.1' "$verify" "$work/ok" "$ver"
	expect "verify: the wrong version" 1 "doesn't hold exactly" "$verify" "$work/ok" 1.0.0
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

	make_release "$work/arch"
	make_deb "$work/arch/drawbridge_${ver}_amd64.deb" "$ver" arm64
	resum "$work/arch"
	expect "verify: a package of another architecture" 1 'has Architecture arm64, not amd64' "$verify" "$work/arch" "$ver"

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
