#!/bin/sh
# Installs Drawbridge from a GitHub release (docs/PLAN.md §11.1): it finds the release, downloads
# the package for this host's architecture and the release's SHA256SUMS, checks the package
# against them, and installs it with apt-get. Installing over an older Drawbridge is the upgrade.
#
# It changes nothing unless the host is Debian-family with systemd running and has an arm64 or
# amd64 package, and nothing before the package's checksum matches and you've said yes. It never
# installs an older version over a newer one: the newer one's database can't be written by an
# older Drawbridge.
#
# Read it before you run it. It needs curl, sha256sum, dpkg, and apt-get, and root (or sudo).
set -eu

repo=stuffam/drawbridge
site=https://github.com/$repo

usage() {
	cat <<'EOF'
Usage: install.sh [--version vX.Y.Z] [--yes]

Installs the latest Drawbridge release (the newest that isn't a pre-release), or the one named
by --version (a pre-release included), after checking its checksum.

  --version vX.Y.Z   install this release instead of the latest
  --yes, -y          don't ask before installing
  --help, -h         show this help
EOF
}

die() {
	echo "install.sh: $*" >&2
	exit 1
}

version='' yes=''
while [ $# -gt 0 ]; do
	case $1 in
	--version)
		[ $# -ge 2 ] || die "--version needs a value, such as v0.1.0"
		version=$2
		shift 2
		;;
	--version=*)
		version=${1#--version=}
		shift
		;;
	--yes | -y)
		yes=1
		shift
		;;
	--help | -h)
		usage
		exit 0
		;;
	*) die "unknown option $1 (try --help)" ;;
	esac
done

# ---- is this a host Drawbridge installs on? ----

[ "$(uname -s)" = Linux ] || die "Drawbridge runs on Linux, and this is $(uname -s)"
for tool in dpkg apt-get; do
	command -v "$tool" >/dev/null 2>&1 ||
		die "this host isn't Debian-family (there's no $tool); Drawbridge ships as a .deb"
done
[ -d /run/systemd/system ] || die "systemd isn't running on this host; Drawbridge's services need it"
arch=$(dpkg --print-architecture)
case $arch in
arm64 | amd64) ;;
*) die "there's no package for $arch; Drawbridge is built for arm64 and amd64" ;;
esac
for tool in curl sha256sum; do
	command -v "$tool" >/dev/null 2>&1 || die "$tool is required"
done
if [ "$(id -u)" = 0 ]; then
	as_root=
elif command -v sudo >/dev/null 2>&1; then
	as_root=sudo
else
	die "run this as root, or install sudo"
fi

# ---- which release? ----

# The latest release's page redirects to its tag, which needs no API, no JSON, and no token. A
# repository with no release redirects to the list of them, and a pre-release is never "latest".
if [ -z "$version" ]; then
	url=$(curl -fsSL -o /dev/null -w '%{url_effective}' "$site/releases/latest") ||
		die "couldn't reach $site"
	tag=${url##*/}
else
	tag=$version
	case $tag in v*) ;; *) tag=v$tag ;; esac
fi
printf '%s\n' "$tag" |
	grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?$' ||
	if [ -z "$version" ]; then
		die "$site has no release yet"
	else
		die "$version isn't a version (such as v0.1.0, or v1.0.0-rc.1)"
	fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
# apt-get downloads as its own user, which can't read a private directory and says so.
chmod 755 "$tmp"
base=$site/releases/download/$tag

curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" ||
	die "couldn't download $tag's SHA256SUMS: is $tag a release at $site/releases?"

# The package for this architecture is the one SHA256SUMS lists. Its name comes from the network,
# so it has to be a package's name and nothing else before it's used in a path or a URL.
file=$(awk -v suffix="_$arch.deb" '
	$2 ~ /^drawbridge_/ && substr($2, length($2) - length(suffix) + 1) == suffix { print $2 }
' "$tmp/SHA256SUMS")
case $file in
*"
"*) die "$tag's SHA256SUMS lists more than one $arch package" ;;
esac
[ -n "$file" ] || die "$tag has no $arch package"
printf '%s\n' "$file" | grep -Eq '^drawbridge_[0-9][0-9A-Za-z.+~-]*_(arm64|amd64)\.deb$' ||
	die "$tag's SHA256SUMS lists a package with an odd name, so it isn't trusted"
new=${file#drawbridge_}
new=${new%_"$arch".deb}

# ---- what's here now? ----

have=$(dpkg-query -W -f='${db:Status-Status} ${Version}' drawbridge 2>/dev/null || true)
case $have in
"installed "*) have=${have#installed } ;;
*) have= ;;
esac
if [ -n "$have" ]; then
	if dpkg --compare-versions "$have" eq "$new"; then
		echo "Drawbridge $new is already installed."
		exit 0
	fi
	if dpkg --compare-versions "$have" gt "$new"; then
		die "Drawbridge $have is installed, and $new is older. An older Drawbridge can't write a newer one's database, so this won't install $new over it."
	fi
fi

# ---- download, check, install ----

curl -fsSL -o "$tmp/$file" "$base/$file" || die "couldn't download $file"
chmod 644 "$tmp/$file"
(cd "$tmp" && awk -v f="$file" '$2 == f' SHA256SUMS | sha256sum -c - >/dev/null 2>&1) ||
	die "$file doesn't match the checksum in $tag's SHA256SUMS, so it isn't installed"
echo "Downloaded $file, and its checksum matches."

if [ -n "$have" ]; then
	echo "Drawbridge $have is installed. This upgrades it to $new."
else
	echo "This installs Drawbridge $new."
fi
if [ -z "$yes" ]; then
	# The script may be coming in on standard input (curl | sh), so the answer is the terminal's.
	( : </dev/tty ) 2>/dev/null || die "there's no terminal to ask on; run it again with --yes"
	printf 'Continue? [y/N] ' >&2
	read -r answer </dev/tty || answer=
	case $answer in
	y | Y | yes | YES) ;;
	*)
		echo "Not installed."
		exit 1
		;;
	esac
fi

cd "$tmp"
# $as_root is empty or "sudo", and is meant to split into nothing or one word.
# shellcheck disable=SC2086
$as_root apt-get install -y "./$file"
