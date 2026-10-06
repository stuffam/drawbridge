#!/bin/sh
# Decides whether a tag may be released, before anything is built (docs/PLAN.md §11.1). It fails,
# saying why, unless:
#
#   - the tag is a version: vMAJOR.MINOR.PATCH, with an optional pre-release suffix such as
#     -rc.1 (no build metadata, which a Debian file name can't carry),
#   - the tag's commit is on main (origin/main, or $MAIN_REF), and
#   - CHANGELOG.md has a section for it (scripts/release-notes.sh).
#
# On success it prints what the workflow needs as key=value lines, for $GITHUB_OUTPUT:
#
#   tag=v1.0.0-rc.1
#   version=1.0.0-rc.1       the version constant, and the OpenAPI version's bare string
#   deb_version=1.0.0~rc.1   the Debian version, which nfpm makes by turning the first - into ~
#   prerelease=true
#
# It runs in the repository, with the tag fetched.
set -eu

[ $# -eq 1 ] || {
	echo "usage: release-check.sh TAG" >&2
	exit 2
}
tag=$1
main=${MAIN_REF:-origin/main}
here=$(dirname "$0")

fail() {
	echo "release-check.sh: $*" >&2
	exit 1
}

# Semantic versioning without build metadata: no leading zeros in a number, and dot-separated
# alphanumeric pre-release identifiers.
if ! printf '%s\n' "$tag" |
	grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?$'; then
	fail "$tag isn't a version (vMAJOR.MINOR.PATCH, optionally -rc.1 and the like)"
fi

commit=$(git rev-parse --verify --quiet "refs/tags/$tag^{commit}") ||
	fail "there's no tag $tag in this clone"
git rev-parse --verify --quiet "$main^{commit}" >/dev/null ||
	fail "can't find $main to check that $tag is on it (the clone needs the whole history)"
git merge-base --is-ancestor "$commit" "$main" ||
	fail "$tag isn't on main: tag a commit that has been merged"

"$here/release-notes.sh" "$tag" "${CHANGELOG:-$here/../CHANGELOG.md}" >/dev/null ||
	fail "CHANGELOG.md needs a section for $tag (a pull request adds it before the tag)"

version=${tag#v}
prerelease=false
case $version in *-*) prerelease=true ;; esac
printf 'tag=%s\nversion=%s\ndeb_version=%s\nprerelease=%s\n' \
	"$tag" "$version" "$(printf '%s' "$version" | sed 's/-/~/')" "$prerelease"
