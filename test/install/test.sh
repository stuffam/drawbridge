#!/usr/bin/env bash
# Tests scripts/install.sh (docs/PLAN.md §11.1) against a fake GitHub release. It runs the real
# script with a PATH that holds only the tools the script uses, and fakes for the rest: curl
# serves a directory as the releases page, apt-get records what it was asked to install and the
# checksum of the file it was handed, and sudo, id, and dpkg's architecture answer as a case asks.
# Nothing is installed or changed on the host, so it needs no root and no container, only a
# Debian-family dpkg, whose version comparison the script uses. `make test-install` runs this.
#
# The script's two paths to the host, /run/systemd/system and /dev/tty, are replaced in a copy
# of it, so a case can say whether systemd is running and what the terminal answers.
set -uo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
for tool in dpkg dpkg-query awk grep sha256sum mktemp chmod rm uname cat; do
	command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 2; }
done

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
bin=$work/bin srv=$work/releases tmpdir=$work/tmp
mkdir -p "$bin" "$srv" "$tmpdir"
site=https://github.com/stuffam/drawbridge

# ---- the PATH the script gets ----

for tool in uname mktemp chmod rm awk grep cat cp cut sha256sum; do
	ln -s "$(command -v "$tool")" "$bin/$tool"
done
real_dpkg=$(command -v dpkg)
real_id=$(command -v id)

cat >"$bin/dpkg" <<EOF
#!/bin/sh
if [ "\$1" = --print-architecture ]; then echo "\$FAKE_ARCH"; else exec "$real_dpkg" "\$@"; fi
EOF
cat >"$bin/id" <<EOF
#!/bin/sh
if [ "\$1" = -u ]; then echo "\$FAKE_UID"; else exec "$real_id" "\$@"; fi
EOF
# dpkg-deb --field FILE Version: the Version line of the fake package, which is a text file.
cat >"$bin/dpkg-deb" <<'EOF'
#!/bin/sh
[ "$1" = --field ] && [ "$3" = Version ] || exit 2
awk -F': ' '$1 == "Version" { print $2 }' "$2"
EOF
# What's installed now: FAKE_INSTALLED is a version, or empty for nothing.
cat >"$bin/dpkg-query" <<'EOF'
#!/bin/sh
[ -n "$FAKE_INSTALLED" ] || exit 1
echo "installed $FAKE_INSTALLED"
EOF
# The releases page and its files, from $FAKE_SRV. It logs every URL it's asked for, refuses
# anything that isn't https from this repository, and fails (like curl -f) on what isn't there.
cat >"$bin/curl" <<'EOF'
#!/bin/sh
out= fmt= url=
while [ $# -gt 0 ]; do
	case $1 in
	-o) out=$2; shift 2 ;;
	-w) fmt=$2; shift 2 ;;
	-*) shift ;;
	*) url=$1; shift ;;
	esac
done
echo "$url" >>"$FAKE_LOG/curl"
site=https://github.com/stuffam/drawbridge
case $url in
"$site/releases/latest")
	if [ -f "$FAKE_SRV/latest" ]; then
		[ "$fmt" = '%{url_effective}' ] && printf '%s' "$site/releases/tag/$(cat "$FAKE_SRV/latest")"
	else
		[ "$fmt" = '%{url_effective}' ] && printf '%s' "$site/releases"
	fi
	exit 0
	;;
"$site/releases/download/"*)
	path=${url#"$site/releases/download/"}
	if [ -f "$FAKE_SRV/$path" ] && [ -n "$out" ]; then cp "$FAKE_SRV/$path" "$out"; exit 0; fi
	exit 22
	;;
esac
echo "unexpected request: $url" >>"$FAKE_LOG/unexpected"
exit 22
EOF
# The install: what it was asked, from where, and the checksum of the file at that moment.
cat >"$bin/apt-get" <<'EOF'
#!/bin/sh
echo "apt-get $*" >>"$FAKE_LOG/apt"
for arg in "$@"; do
	case $arg in
	./*) echo "file $arg $(sha256sum "$arg" | cut -d' ' -f1) in $(pwd)" >>"$FAKE_LOG/apt" ;;
	esac
done
exit "${FAKE_APT_EXIT:-0}"
EOF
cat >"$bin/sudo" <<'EOF'
#!/bin/sh
echo "sudo $*" >>"$FAKE_LOG/apt"
exec "$@"
EOF
chmod +x "$bin/dpkg" "$bin/dpkg-deb" "$bin/id" "$bin/dpkg-query" "$bin/curl" "$bin/apt-get" "$bin/sudo"

# ---- a fake release page ----

# release TAG VERSION [ARCH...]: the files of a release, and the SHA256SUMS that lists them along
# with the files that aren't packages. The files are named for the version (1.0.0-rc.1), and each
# package declares the Debian form (1.0.0~rc.1), as the real ones do.
release() {
	local tag=$1 ver=$2 arch d debver=$2
	shift 2
	case $ver in *-*) debver=${ver%%-*}~${ver#*-} ;; esac
	d=$srv/$tag
	mkdir -p "$d"
	for arch in "${@:-arm64 amd64}"; do
		for a in $arch; do
			printf 'package %s %s\nVersion: %s\n' "$tag" "$a" "$debver" >"$d/drawbridge_${ver}_$a.deb"
			echo '{"bomFormat":"CycloneDX"}' >"$d/drawbridge_${ver}_$a.sbom.json"
		done
	done
	echo '#!/bin/sh' >"$d/install.sh"
	(cd "$d" && for f in *; do echo "$f"; done | LC_ALL=C sort | xargs sha256sum >"$work/sums" && mv "$work/sums" SHA256SUMS)
}

# ---- running the script ----

passed=0 failed=0
pass() { passed=$((passed + 1)); }
fail() {
	failed=$((failed + 1))
	echo "FAIL: $1" >&2
	[ -z "${2:-}" ] || printf '%s\n' "$2" | sed 's/^/      /' >&2
}

systemd_dir=$work/systemd
tty_file=$work/tty
out='' status=''
# run ARGS...: the script, with the environment a case set before it. Output and status are left
# in $out and $status.
run() {
	local script=$work/install.sh
	sed -e "s|/run/systemd/system|$systemd_dir|g" -e "s|/dev/tty|$tty_file|g" \
		"$root/scripts/install.sh" >"$script"
	if ! grep -q "$systemd_dir" "$script" || ! grep -q "$tty_file" "$script"; then
		fail "the test's copy of install.sh doesn't carry its paths"
	fi
	rm -rf "$work/log" "$tmpdir"
	mkdir -p "$work/log" "$tmpdir"
	: >"$work/log/apt"
	: >"$work/log/curl"
	out=$(env -i PATH="$bin" TMPDIR="$tmpdir" FAKE_SRV="$srv" FAKE_LOG="$work/log" \
		FAKE_ARCH="${FAKE_ARCH:-arm64}" FAKE_UID="${FAKE_UID:-0}" FAKE_INSTALLED="${FAKE_INSTALLED:-}" \
		FAKE_APT_EXIT="${FAKE_APT_EXIT:-0}" /bin/sh "$script" "$@" 2>&1)
	status=$?
}
apt_calls() { grep -c '^apt-get ' "$work/log/apt" || true; }
requests() { cat "$work/log/curl"; }
# logged NAME REGEX: apt-get's log has a line matching REGEX. not_logged: it has none.
logged() {
	if grep -Eq -- "$2" "$work/log/apt"; then pass; else fail "$1" "$(cat "$work/log/apt")"; fi
}
not_logged() {
	if grep -Eq -- "$2" "$work/log/apt"; then fail "$1" "$(cat "$work/log/apt")"; else pass; fi
}

# Each case begins from the same host: root on arm64 with systemd and nothing installed, a page
# whose latest release is v0.2.0, and no terminal.
reset() {
	rm -rf "$srv"
	mkdir -p "$srv" "$systemd_dir"
	rm -f "$tty_file"
	release v0.2.0 0.2.0
	release v0.1.0 0.1.0
	release v1.0.0-rc.1 1.0.0-rc.1
	echo v0.2.0 >"$srv/latest"
	FAKE_ARCH=arm64 FAKE_UID=0 FAKE_INSTALLED='' FAKE_APT_EXIT=0
}

# ok NAME REGEX ARGS...: the script succeeds and says something matching REGEX.
# refuses NAME REGEX ARGS...: it fails, says so, and never reached apt-get.
ok() {
	local name=$1 re=$2
	shift 2
	run "$@"
	if [ "$status" != 0 ]; then fail "$name: exited $status" "$out"
	elif ! grep -Eq -- "$re" <<<"$out"; then fail "$name: output doesn't match /$re/" "$out"
	else pass; fi
}
refuses() {
	local name=$1 re=$2
	shift 2
	run "$@"
	if [ "$status" = 0 ]; then fail "$name: succeeded, but should have refused" "$out"
	elif ! grep -Eq -- "$re" <<<"$out"; then fail "$name: output doesn't match /$re/" "$out"
	elif [ "$(apt_calls)" != 0 ]; then fail "$name: reached apt-get" "$(cat "$work/log/apt")"
	else pass; fi
}
same() {
	if [ "$2" = "$3" ]; then pass; else fail "$1" "want: $2
got:  $3"; fi
}
sum_of() { sha256sum "$1" | cut -d' ' -f1; }

# ---- the happy paths ----

reset
ok "latest, arm64: installs the newest release" 'This installs Drawbridge 0.2.0' --yes
same "latest, arm64: one apt-get call" 1 "$(apt_calls)"
logged "latest, arm64: apt-get is asked for the arm64 package" '^apt-get install -y ./drawbridge_0.2.0_arm64.deb$'
logged "latest, arm64: apt-get gets the file the release has" \
	"^file ./drawbridge_0.2.0_arm64.deb $(sum_of "$srv/v0.2.0/drawbridge_0.2.0_arm64.deb") in "
same "only this repository's releases are asked for, over https" \
	"$site/releases/latest
$site/releases/download/v0.2.0/SHA256SUMS
$site/releases/download/v0.2.0/drawbridge_0.2.0_arm64.deb" "$(requests)"
if [ -e "$work/log/unexpected" ]; then fail "a request went elsewhere" "$(cat "$work/log/unexpected")"; else pass; fi
same "the temporary directory is removed" "" "$(ls -A "$tmpdir")"

reset
FAKE_ARCH=amd64 ok "amd64 gets the amd64 package" 'Downloaded drawbridge_0.2.0_amd64.deb' --yes
logged "amd64: apt-get is asked for the amd64 package" '^apt-get install -y ./drawbridge_0.2.0_amd64.deb$'

reset
ok "--version pins an older release" 'This installs Drawbridge 0.1.0' --version v0.1.0 --yes
ok "--version without the v" 'This installs Drawbridge 0.1.0' --version 0.1.0 --yes
ok "--version=" 'This installs Drawbridge 0.1.0' --version=v0.1.0 --yes
ok "-y" 'This installs Drawbridge 0.2.0' -y

reset
ok "--version names a pre-release" 'This installs Drawbridge 1.0.0~rc.1' --version v1.0.0-rc.1 --yes
logged "pre-release: apt-get is asked for the pre-release's package, named for its version" '^apt-get install -y ./drawbridge_1.0.0-rc.1_arm64.deb$'

reset
ok "latest ignores the pre-release that exists" 'This installs Drawbridge 0.2.0' --yes

reset
FAKE_INSTALLED=0.1.0 ok "an upgrade says what it replaces" 'Drawbridge 0.1.0 is installed. This upgrades it to 0.2.0' --yes
same "an upgrade: apt-get is called" 1 "$(apt_calls)"
reset
FAKE_INSTALLED=0.2.0 ok "the same version is already installed" 'Drawbridge 0.2.0 is already installed' --yes
same "the same version: apt-get isn't called" 0 "$(apt_calls)"
reset
release v1.0.0 1.0.0
FAKE_INSTALLED='1.0.0~rc.1' ok "a pre-release is upgraded to the release after it" 'upgrades it to 1.0.0' --version v1.0.0 --yes
reset
FAKE_INSTALLED='1.0.0~rc.1' ok "the pre-release that's installed is the same version, though its file is named 1.0.0-rc.1" 'Drawbridge 1.0.0~rc.1 is already installed' --version v1.0.0-rc.1 --yes
same "...so apt-get isn't called" 0 "$(apt_calls)"
reset
FAKE_INSTALLED='1.0.0' refuses "a pre-release isn't installed over its release" 'is older' --version v1.0.0-rc.1 --yes

# ---- root and sudo ----

reset
FAKE_UID=1000
ok "not root: through sudo" 'This installs' --yes
logged "not root: apt-get runs through sudo" '^sudo apt-get install -y ./drawbridge_0.2.0_arm64.deb$'
reset
ok "root: without sudo" 'This installs' --yes
not_logged "root: no sudo" '^sudo'
reset
FAKE_UID=1000
mv "$bin/sudo" "$work/sudo.away"
refuses "not root, and no sudo" 'run this as root, or install sudo' --yes
mv "$work/sudo.away" "$bin/sudo"

# ---- the terminal ----

reset
refuses "no --yes and no terminal" 'no terminal to ask on; run it again with --yes'
reset
echo y >"$tty_file"
ok "the terminal says y" 'This installs Drawbridge 0.2.0'
same "y: installed" 1 "$(apt_calls)"
reset
echo n >"$tty_file"
refuses "the terminal says n" 'Not installed'
reset
: >"$tty_file"
refuses "the terminal says nothing" 'Not installed'

# ---- hosts it refuses, before it asks the network anything ----

reset
FAKE_ARCH=armhf
refuses "an architecture without a package" 'no package for armhf' --yes
same "...and it asked for nothing" "" "$(requests)"
reset
rmdir "$systemd_dir"
refuses "systemd isn't running" "systemd isn't running" --yes
same "...and it asked for nothing" "" "$(requests)"
reset
mv "$bin/apt-get" "$work/apt-get.away"
refuses "not Debian-family" "isn't Debian-family" --yes
mv "$work/apt-get.away" "$bin/apt-get"
same "...and it asked for nothing" "" "$(requests)"
reset
mv "$bin/curl" "$work/curl.away"
refuses "no curl" 'curl is required' --yes
mv "$work/curl.away" "$bin/curl"

# ---- what's wrong with a release ----

reset
rm "$srv/latest"
refuses "no release yet" 'has no release yet' --yes
reset
refuses "a version that isn't one" "isn't a version" --version latest --yes
refuses "a version with a v only" "isn't a version" --version v1.2 --yes
refuses "a version that isn't a release" "couldn't download v9.9.9's SHA256SUMS" --version v9.9.9 --yes
reset
rm "$srv/v0.2.0/drawbridge_0.2.0_arm64.deb"
refuses "SHA256SUMS lists a file the release doesn't have" "couldn't download drawbridge_0.2.0_arm64.deb" --yes
reset
echo "tampered" >>"$srv/v0.2.0/drawbridge_0.2.0_arm64.deb"
refuses "a package that doesn't match its checksum" "doesn't match the checksum" --yes
same "...so nothing is installed" 0 "$(apt_calls)"
# A package whose checksum matches but which declares another version than its name says.
resum() { (cd "$1" && for f in *; do [ "$f" = SHA256SUMS ] || echo "$f"; done | LC_ALL=C sort | xargs sha256sum >"$work/sums" && mv "$work/sums" SHA256SUMS); }
reset
printf 'package v0.2.0 arm64\nVersion: 9.9.9\n' >"$srv/v0.2.0/drawbridge_0.2.0_arm64.deb"
resum "$srv/v0.2.0"
refuses "a package that declares another version than its name" 'declares version 9.9.9, not 0.2.0' --yes
same "...so nothing is installed" 0 "$(apt_calls)"
reset
printf 'package v1.0.0-rc.1 arm64\nVersion: 1.0.0-rc.1\n' >"$srv/v1.0.0-rc.1/drawbridge_1.0.0-rc.1_arm64.deb"
resum "$srv/v1.0.0-rc.1"
refuses "a pre-release package with a - in its Version, which would sort after its release" 'declares version 1.0.0-rc.1, not 1.0.0~rc.1' --version v1.0.0-rc.1 --yes
reset
printf 'package v0.2.0 arm64\n' >"$srv/v0.2.0/drawbridge_0.2.0_arm64.deb"
resum "$srv/v0.2.0"
refuses "a package that declares no version" 'declares version nothing, not 0.2.0' --yes
reset
rm "$srv/v0.2.0/SHA256SUMS"
refuses "no SHA256SUMS" "couldn't download v0.2.0's SHA256SUMS" --yes
reset
release v0.3.0 0.3.0 amd64
echo v0.3.0 >"$srv/latest"
refuses "no package for this architecture" 'has no arm64 package' --yes
reset
printf '%s  drawbridge_../../etc/passwd_arm64.deb\n' "$(printf x | sha256sum | cut -d' ' -f1)" >"$srv/v0.2.0/SHA256SUMS"
refuses "a package name with a path in it" 'odd name' --yes
reset
printf '%s  drawbridge_0.2.0;x_arm64.deb\n' "$(printf x | sha256sum | cut -d' ' -f1)" >"$srv/v0.2.0/SHA256SUMS"
refuses "a package name with a shell character in it" 'odd name' --yes
reset
{
	cat "$srv/v0.2.0/SHA256SUMS"
	printf '%s  drawbridge_0.2.1_arm64.deb\n' "$(printf x | sha256sum | cut -d' ' -f1)"
} >"$work/sums" && mv "$work/sums" "$srv/v0.2.0/SHA256SUMS"
refuses "two packages for one architecture" 'more than one arm64 package' --yes

# ---- never an older one over a newer one ----

reset
FAKE_INSTALLED=0.3.0
refuses "a downgrade" 'Drawbridge 0.3.0 is installed, and 0.2.0 is older' --yes
reset
FAKE_INSTALLED=1.0.0
refuses "an installed release isn't replaced by its own pre-release" 'is older' --version v1.0.0-rc.1 --yes

# ---- apt-get fails; the options ----

reset
FAKE_APT_EXIT=100
run --yes
if [ "$status" != 0 ]; then pass; else fail "a failing apt-get is a failing install" "$out"; fi

reset
ok "--help" 'Usage: install.sh' --help
refuses "an unknown option" 'unknown option --nope' --nope
refuses "--version without a value" '--version needs a value' --version

echo "$passed passed, $failed failed"
[ "$failed" = 0 ]
