#!/bin/sh
# Prints a version's section of CHANGELOG.md, without its heading: the text between the heading
# `## [vX.Y.Z] — 2026-10-05` (Keep a Changelog's form, and the project's; the brackets and the date
# are optional) and the next `## ` heading, with what closes the section trimmed: blank lines, a `---`
# line between releases, and the link definitions at the foot of the file (`[vX.Y.Z]: https://...`).
# The `###` headings inside it (Added, Fixed, and so on) are part of it. It fails when there's no such
# section, or it has no text. A link definition in the middle of a section stays, but one at the end
# goes with the footer, so a section's own links should be inline.
#
# --unwrap is for a release's description. GitHub's release page shows every newline in it as a line
# break, and the changelog is wrapped at 100 columns, so the notes would come out ragged. It joins
# the lines of a paragraph or a list item into one. A line that starts a block of its own stays as
# written (a heading, a list item, a rule, a table row, a quote, an HTML line, a link definition),
# and so do a fenced code block and a line that ends in a hard break (two spaces or a backslash). An
# indented code block isn't known: use a fence.
#
#   scripts/release-notes.sh [--unwrap] v1.0.0 [CHANGELOG.md]
set -eu

usage() {
	echo "usage: release-notes.sh [--unwrap] VERSION [CHANGELOG]" >&2
	exit 2
}
unwrap=
if [ "${1:-}" = --unwrap ]; then
	unwrap=1
	shift
fi
if [ $# -lt 1 ] || [ $# -gt 2 ]; then usage; fi
case $1 in -*) usage ;; esac
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

# Join the lines of each paragraph and list item (see the top). `cur` is the block being built, and
# it is printed when the next line can't continue it.
if [ -n "$unwrap" ]; then
	notes=$(printf '%s\n' "$notes" | awk '
		function flush() { if (have) print cur; have = 0 }
		/^[[:space:]]*(```|~~~)/ { if (!infence) flush(); print; infence = !infence; next }
		infence { print; next }
		/^[[:space:]]*$/ { flush(); print ""; next }
		/^[[:space:]]*(#+([[:space:]]|$)|\||>|<)/ || /^[[:space:]]*(---+|\*\*\*+|___+)[[:space:]]*$/ || /^\[[^]]+\]:[[:space:]]/ {
			flush(); print; next
		}
		/^[[:space:]]*([-*+]|[0-9]+[.)])[[:space:]]/ { flush(); cur = $0; have = 1; next }
		!have { cur = $0; have = 1; next }
		cur ~ /(  |\\)$/ { print cur; cur = $0; next }
		{
			line = $0
			sub(/^[[:space:]]+/, "", line)
			sub(/[[:space:]]+$/, "", cur)
			cur = cur " " line
		}
		END { flush() }
	')
fi
printf '%s\n' "$notes"
