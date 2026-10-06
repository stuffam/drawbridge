#!/bin/sh
# Checks what `make release-files` made, before it's published (docs/PLAN.md §11.1): that the
# directory holds exactly a release's files, that SHA256SUMS covers all of them and matches, and
# that each package is what its name says and declares what it needs. It needs dpkg-deb.
#
#   scripts/release-verify.sh DIR DEB_VERSION     DEB_VERSION is 0.1.0, or 1.0.0~rc.1
#
# A release holds, for the Debian version V:
#
#   drawbridge_V_arm64.deb       drawbridge_V_amd64.deb
#   drawbridge_V_arm64.sbom.json drawbridge_V_amd64.sbom.json    the Go modules in each binary
#   drawbridge-web_V.sbom.json                                   the web app's npm packages
#   install.sh
#   SHA256SUMS                   the checksum of every file above
set -eu

[ $# -eq 2 ] || {
	echo "usage: release-verify.sh DIR DEB_VERSION" >&2
	exit 2
}
dir=$1
ver=$2
command -v dpkg-deb >/dev/null 2>&1 || {
	echo "release-verify.sh: dpkg-deb is required" >&2
	exit 2
}

bad=
problem() {
	echo "release-verify.sh: $*" >&2
	bad=1
}

want=$(
	cat <<EOF | LC_ALL=C sort
SHA256SUMS
drawbridge-web_${ver}.sbom.json
drawbridge_${ver}_amd64.deb
drawbridge_${ver}_amd64.sbom.json
drawbridge_${ver}_arm64.deb
drawbridge_${ver}_arm64.sbom.json
install.sh
EOF
)
have=$(cd "$dir" && find . -mindepth 1 -maxdepth 1 | sed 's|^\./||' | LC_ALL=C sort)
if [ "$have" != "$want" ]; then
	problem "$dir doesn't hold exactly a release's files"
	echo "--- expected" >&2
	printf '%s\n' "$want" >&2
	echo "--- found" >&2
	printf '%s\n' "$have" >&2
	exit 1
fi

# SHA256SUMS names every other file once, and each one matches.
listed=$(awk '{ print $2 }' "$dir/SHA256SUMS" | LC_ALL=C sort)
others=$(printf '%s\n' "$want" | grep -vx SHA256SUMS)
[ "$listed" = "$others" ] || problem "SHA256SUMS doesn't list exactly the other six files"
(cd "$dir" && sha256sum -c --quiet SHA256SUMS) || problem "SHA256SUMS doesn't match the files"

for arch in arm64 amd64; do
	deb=$dir/drawbridge_${ver}_${arch}.deb
	[ "$(dpkg-deb --field "$deb" Package)" = drawbridge ] || problem "$deb isn't the drawbridge package"
	[ "$(dpkg-deb --field "$deb" Version)" = "$ver" ] ||
		problem "$deb has Version $(dpkg-deb --field "$deb" Version), not $ver"
	[ "$(dpkg-deb --field "$deb" Architecture)" = "$arch" ] ||
		problem "$deb has Architecture $(dpkg-deb --field "$deb" Architecture), not $arch"
	# What the package needs on the host (docs/PLAN.md §11): nft applies its firewall, and a time
	# daemon keeps the clock that certificates, handshakes, and two-factor codes go by. Without
	# the dependency, an install succeeds on a host where the VPN then can't come up.
	dpkg-deb --field "$deb" Depends | grep -Eq '(^|, )nftables($|,| )' ||
		problem "$deb doesn't depend on nftables"
	dpkg-deb --field "$deb" Recommends | grep -qF 'systemd-timesyncd | time-daemon' ||
		problem "$deb doesn't recommend systemd-timesyncd | time-daemon"
	dpkg-deb --field "$deb" Recommends | grep -Eq '(^|, )wireguard-tools($|,| )' ||
		problem "$deb doesn't recommend wireguard-tools"
done

for sbom in "$dir"/*.sbom.json; do
	grep -q '"bomFormat"' "$sbom" || problem "$sbom isn't a CycloneDX document"
done
[ -x "$dir/install.sh" ] || problem "$dir/install.sh isn't executable"

[ -z "$bad" ] || exit 1
echo "release files OK: drawbridge $ver"
