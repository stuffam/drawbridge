#!/bin/sh
# Decides what the docs site publishes for a release tag (docs/adr/0013-docs-site-zensical.md). The
# site keeps one entry for each minor release, so v0.1.0 and v0.1.1 are both "v0.1":
#
#   docs-version.sh TAG           prints the docs version of a final release, vMAJOR.MINOR. It
#                                 never reads git.
#   docs-version.sh --latest TAG  prints nothing, and exits 0 when TAG is the highest final
#                                 release tag in this clone, 1 when a higher one exists. A patch
#                                 for an older minor (v0.1.5 after v0.2.0) then refreshes its own
#                                 entry and leaves the `latest` alias where it is.
#
# "Highest" goes by the tags in the clone and not by which releases are published: a final tag with
# no published release (a draft, an abandoned tag) still counts, so a lower release leaves `latest`
# alone. docs/releasing.md ("Docs versions") has the command that moves it by hand.
#
# Only a final vMAJOR.MINOR.PATCH publishes: a pre-release such as v0.2.0-rc.1 has no docs of its
# own, and a tag is a name from the network, so anything else is refused before it can reach mike.
# --latest runs in the repository, with the tags fetched.
set -eu

usage() {
	echo "usage: docs-version.sh [--latest] TAG" >&2
	exit 2
}

fail() {
	echo "docs-version.sh: $*" >&2
	exit 1
}

latest=false
case $# in
1) tag=$1 ;;
2)
	[ "$1" = --latest ] || usage
	latest=true
	tag=$2
	;;
*) usage ;;
esac

# No leading zeros in a number, and no suffix. grep works a line at a time, so a newline would let a
# second line through: refuse it first.
final='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
case $tag in
*'
'*) fail "$tag isn't a release version (vMAJOR.MINOR.PATCH, with no suffix)" ;;
esac
printf '%s\n' "$tag" | grep -Eq "$final" ||
	fail "$tag isn't a release version (vMAJOR.MINOR.PATCH, with no suffix)"

if [ "$latest" = false ]; then
	printf '%s\n' "${tag%.*}"
	exit 0
fi

git rev-parse --verify --quiet "refs/tags/$tag" >/dev/null ||
	fail "there's no tag $tag in this clone"
highest=$(git tag --list --sort=-version:refname 'v*' | grep -E "$final" | head -n 1)
[ "$highest" = "$tag" ] || exit 1
