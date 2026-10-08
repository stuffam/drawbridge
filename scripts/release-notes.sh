#!/bin/sh
# Prints a version's section of CHANGELOG.md, without its heading: the text between the heading
# `## [vX.Y.Z] — 2026-10-05` (Keep a Changelog's form, and the project's; the brackets and the date
# are optional) and the next `## ` heading, with what closes the section trimmed: blank lines, a `---`
# line between releases, and the link definitions at the foot of the file (`[vX.Y.Z]: https://...`).
# The `###` headings inside it (Added, Fixed, and so on) are part of it. It fails when there's no such
# section, or it has no text. A link definition in the middle of a section stays, but one at the end
# goes with the footer, so a section's own links should be inline.
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
	$1 == "##" {
		if (found) exit
		version = $2
		gsub(/^\[|\]$/, "", version)
		if (version == tag) { found = 1; next }
	}
	found { print }
' "$file")

# Trim the blank lines at the start, and at the end the blank lines, `---` separators, and link
# definitions that close a section. Fail on a section with nothing in it.
notes=$(printf '%s\n' "$notes" | awk '
	NF { started = 1 }
	started { lines[++n] = $0 }
	END {
		while (n > 0 && (lines[n] !~ /[^[:space:]]/ || lines[n] ~ /^---+[[:space:]]*$/ || lines[n] ~ /^\[[^]]+\]:[[:space:]]/)) n--
		for (i = 1; i <= n; i++) print lines[i]
	}
')
if [ -z "$notes" ]; then
	echo "release-notes.sh: $file has no section, or an empty one, for $tag" >&2
	exit 1
fi
printf '%s\n' "$notes"
