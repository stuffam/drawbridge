#!/bin/sh
# Prints the link to the install guide for a release's notes (docs/adr/0013-docs-site-zensical.md).
# The notes of a published release can't change, so the link has to stay right:
#
#   - A final release (v0.1.3) links to its own version of the docs site (v0.1), which stays right
#     after `latest` moves on:  https://stuffam.github.io/drawbridge/v0.1/getting-started/
#   - A pre-release (v0.2.0-rc.1) has no docs version of its own, so it links to the guide as the
#     tag has it, on GitHub.
#
# A tag is a name from the network, so anything that isn't a version is refused before it can reach
# a URL. It never reads git.
#
# usage: release-install-url.sh TAG
set -eu

site=https://stuffam.github.io/drawbridge
repo=stuffam/drawbridge

[ $# -eq 1 ] || {
	echo "usage: release-install-url.sh TAG" >&2
	exit 2
}
tag=$1

fail() {
	echo "release-install-url.sh: $*" >&2
	exit 1
}

# Semantic versioning without build metadata, as release-check.sh reads it. grep works a line at a
# time, so a newline would let a second line through: refuse it first.
case $tag in
*'
'*) fail "$tag isn't a version (vMAJOR.MINOR.PATCH, optionally -rc.1 and the like)" ;;
esac
printf '%s\n' "$tag" |
	grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?$' ||
	fail "$tag isn't a version (vMAJOR.MINOR.PATCH, optionally -rc.1 and the like)"

case $tag in
*-*) printf 'https://github.com/%s/blob/%s/docs/user_docs/getting-started.md\n' "$repo" "$tag" ;;
*) printf '%s/%s/getting-started/\n' "$site" "${tag%.*}" ;;
esac
