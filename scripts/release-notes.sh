#!/bin/sh
# Prints a version's section of CHANGELOG.md, without its heading: the text between the heading
# `## vX.Y.Z` (optionally followed by a space and a date) and the next `## ` heading, with the
# blank lines around it trimmed. It fails when there's no such section, or it has no text.
#
#   scripts/release-notes.sh v1.0.0 [CHANGELOG.md]
set -eu

if [ $# -lt 1 ] || [ $# -gt 2 ]; then
	echo "usage: release-notes.sh VERSION [CHANGELOG]" >&2
	exit 2
fi
tag=$1
file=${2:-$(dirname "$0")/../CHANGELOG.md}
[ -r "$file" ] || {
	echo "release-notes.sh: can't read $file" >&2
	exit 1
}

notes=$(awk -v tag="$tag" '
	$1 == "##" { if (found) exit; if ($2 == tag) { found = 1; next } }
	found { print }
' "$file")

# Trim the blank lines at both ends, and fail on a section with nothing in it.
notes=$(printf '%s\n' "$notes" | awk '
	NF { started = 1 }
	started { lines[++n] = $0 }
	END {
		while (n > 0 && lines[n] !~ /[^[:space:]]/) n--
		for (i = 1; i <= n; i++) print lines[i]
	}
')
if [ -z "$notes" ]; then
	echo "release-notes.sh: $file has no section, or an empty one, for $tag" >&2
	exit 1
fi
printf '%s\n' "$notes"
