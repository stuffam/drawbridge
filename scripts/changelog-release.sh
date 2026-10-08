#!/bin/sh
# Turns CHANGELOG.md's [Unreleased] section into a release's section, for the pull request that
# comes before a tag (docs/PLAN.md §11.1, docs/releasing.md):
#
#   - `## [Unreleased]` becomes `## [vX.Y.Z] — DATE`, with a fresh, empty `## [Unreleased]` and a
#     `---` above it, and
#   - the links at the foot of the file are rebuilt, newest first: [Unreleased] compares the newest
#     version with main, each version compares itself with the one below it, and the oldest links to
#     its release page.
#
# It changes nothing, and fails saying why, unless the version is a version (vMAJOR.MINOR.PATCH, with
# an optional pre-release suffix such as -rc.1), the date is YYYY-MM-DD, the changelog has no section
# for that version yet, and [Unreleased] has something under it besides its `###` headings.
#
#   scripts/changelog-release.sh v1.0.0 [YYYY-MM-DD] [CHANGELOG.md]
#
# The date is today's, in UTC, when left out. REPO_URL is the repository's address for the links
# (https://github.com/owner/repo); without it the script reads it from the [Unreleased] link already
# at the foot of the file. The branch the links compare with is main, or BASE_BRANCH.
set -eu

if [ $# -lt 1 ] || [ $# -gt 3 ]; then
	echo "usage: changelog-release.sh VERSION [DATE] [CHANGELOG]" >&2
	exit 2
fi
tag=$1
date=${2:-$(date -u +%Y-%m-%d)}
file=${3:-$(dirname "$0")/../CHANGELOG.md}
branch=${BASE_BRANCH:-main}

fail() {
	echo "changelog-release.sh: $*" >&2
	exit 1
}

# The same shape release-check.sh accepts for a tag.
printf '%s\n' "$tag" |
	grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?$' ||
	fail "$tag isn't a version (vMAJOR.MINOR.PATCH, optionally -rc.1 and the like)"
printf '%s\n' "$date" | grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2}$' ||
	fail "the date must be YYYY-MM-DD, not $date"
[ -r "$file" ] || fail "can't read $file"

base=${REPO_URL:-}
[ -n "$base" ] || base=$(sed -n 's|^\[Unreleased\]:[[:space:]]*\(.*\)/compare/.*$|\1|p' "$file" | head -n 1)
[ -n "$base" ] || fail "no REPO_URL, and no [Unreleased] link at the foot of $file to read it from"
base=${base%/}

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

# Everything is held until the end, so a refusal leaves nothing half written.
awk -v tag="$tag" -v date="$date" -v base="$base" -v branch="$branch" '
	function version(s) { gsub(/^\[|\]$/, "", s); return s }
	BEGIN { n = 0; nv = 0 }
	$1 == "##" {
		v = version($2)
		in_unreleased = 0
		if (v == tag) err = "the changelog already has a section for " tag
		if (v == "Unreleased") {
			if (seen) err = "the changelog has more than one [Unreleased] section"
			seen = 1
			in_unreleased = 1
			out[++n] = "## [Unreleased]"
			out[++n] = ""
			out[++n] = "---"
			out[++n] = ""
			out[++n] = "## [" tag "] — " date
			vers[++nv] = tag
			next
		}
		if (v ~ /^v[0-9]/) vers[++nv] = v
	}
	# Text under [Unreleased]: not a blank line, a separator, or a ### heading with nothing under it.
	in_unreleased && $0 ~ /[^[:space:]]/ && $0 !~ /^---+[[:space:]]*$/ && $0 !~ /^#/ { has = 1 }
	{ out[++n] = $0 }
	END {
		if (err == "" && !seen) err = "the changelog has no [Unreleased] section"
		if (err == "" && !has) err = "[Unreleased] has nothing under it to release"
		if (err != "") {
			print "changelog-release.sh: " err > "/dev/stderr"
			exit 1
		}
		# What closes the file is rebuilt: blank lines, a separator, and the old links.
		while (n > 0 && (out[n] !~ /[^[:space:]]/ || out[n] ~ /^---+[[:space:]]*$/ || out[n] ~ /^\[[^]]+\]:[[:space:]]/)) n--
		for (i = 1; i <= n; i++) print out[i]
		print ""
		print "---"
		print ""
		print "[Unreleased]: " base "/compare/" vers[1] "..." branch
		for (i = 1; i <= nv; i++) {
			if (i < nv) print "[" vers[i] "]: " base "/compare/" vers[i + 1] "..." vers[i]
			else print "[" vers[i] "]: " base "/releases/tag/" vers[i]
		}
	}
' "$file" >"$tmp"

# Written through cat, so the file keeps its mode.
cat "$tmp" >"$file"
echo "Turned [Unreleased] into [$tag] — $date in $file, with a fresh [Unreleased] above it."
