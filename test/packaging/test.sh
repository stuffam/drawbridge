#!/usr/bin/env bash
# Runs the package's real maintainer scripts (packaging/deb/{postinst,prerm,postrm}) through real
# dpkg, for a first install, an upgrade, a downgrade, a remove, a reinstall, and a purge, and
# checks what each one does to the two units (docs/PLAN.md §11): an admin's `systemctl disable`
# survives an upgrade, `remove` keeps the units enabled, a daemon that won't start doesn't leave
# the package half-configured, and the tunnel is never restarted (ADR 0008).
#
# systemd isn't involved. A fake systemctl, first on PATH, models the part of it the scripts use:
# a unit is enabled when its link in multi-user.target.wants resolves to a unit file, the way
# systemd reads it, and active when a marker says so. It records every call, so a test can say
# what the scripts asked for as well as what became of the units. sysctl, modprobe, and sleep are
# fake too, so the host is untouched. `make test-packaging` runs this.
#
# It installs and purges a package named drawbridge, and creates and deletes /etc/drawbridge and
# /var/lib/drawbridge, so it needs root, dpkg, and a throwaway Debian container or VM, and it
# refuses to run on a host that has any of them already:
#
#   docker run --rm -v "$PWD":/work:ro -e DRAWBRIDGE_PACKAGING_TEST=1 debian:13-slim \
#     /work/test/packaging/test.sh
set -uo pipefail

if [ "${DRAWBRIDGE_PACKAGING_TEST:-}" != 1 ]; then
	echo "this installs and purges a package named drawbridge; run it only in a throwaway container or VM" >&2
	echo "(DRAWBRIDGE_PACKAGING_TEST=1 says you did)" >&2
	exit 2
fi
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 2; }
for tool in dpkg dpkg-deb perl; do
	command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 2; }
done
if compgen -G '/var/lib/dpkg/info/drawbridge.*' >/dev/null || [ -e /etc/drawbridge ] ||
	[ -e /var/lib/drawbridge ] || [ -L /etc/systemd/system/multi-user.target.wants/drawbridge.service ]; then
	echo "drawbridge is already on this host (installed, or left behind); not touching it" >&2
	exit 2
fi

root=$(cd "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
state=$work/state
wants=/etc/systemd/system/multi-user.target.wants
made_systemd_dir= made_user=

cleanup() {
	dpkg -P drawbridge >/dev/null 2>&1
	rm -rf /etc/drawbridge /var/lib/drawbridge /run/drawbridge "$work"
	rm -f "$wants/drawbridge.service" "$wants/drawbridge-tunnel.service"
	[ -n "$made_systemd_dir" ] && rmdir /run/systemd/system /run/systemd 2>/dev/null
	[ -n "$made_user" ] && userdel drawbridge 2>/dev/null
	return 0
}
trap cleanup EXIT

export FAKE_SYSTEMCTL_STATE=$state
export PATH="$work/bin:$PATH"
mkdir -p "$work/bin" "$state"

# The scripts do their service work only where systemd runs, which they tell by this directory.
if [ ! -d /run/systemd/system ]; then
	mkdir -p /run/systemd/system
	made_systemd_dir=1
fi
# What postinst's sysusers step would make; the package's own group is needed for /etc/drawbridge.
if ! getent passwd drawbridge >/dev/null; then
	groupadd --system drawbridge && useradd --system --gid drawbridge --no-create-home \
		--home-dir /var/lib/drawbridge --shell /usr/sbin/nologin drawbridge
	made_user=1
fi

# ---- the fakes ----

cat >"$work/bin/systemctl" <<'EOF'
#!/bin/sh
state=$FAKE_SYSTEMCTL_STATE
echo "$*" >>"$state/calls"
wants=/etc/systemd/system/multi-user.target.wants
unitdir=/usr/lib/systemd/system
sock=/run/drawbridge/control.sock

cmd= now=0 units=
for arg in "$@"; do
	case $arg in
	--now) now=1 ;;
	--*) ;;
	*) if [ -z "$cmd" ]; then cmd=$arg; else units="$units $arg"; fi ;;
	esac
done

loaded() { [ -f "$unitdir/$1" ]; }
stop() {
	rm -f "$state/active/$1"
	[ "$1" = drawbridge.service ] && rm -f "$sock"
	return 0
}
start() {
	touch "$state/active/$1"
	if [ "$1" = drawbridge.service ]; then
		# The daemon's control socket: only that it exists is read.
		mkdir -p /run/drawbridge
		perl -e 'use Socket; socket(S, PF_UNIX, SOCK_STREAM, 0) || die; unlink $ARGV[0]; bind(S, sockaddr_un($ARGV[0])) || die' "$sock"
	fi
}

case $cmd in
daemon-reload) ;;
is-active)
	for u in $units; do [ -e "$state/active/$u" ] || exit 3; done ;;
is-enabled)
	# A link to a unit file that's gone, as `remove` leaves one, doesn't count.
	for u in $units; do [ -e "$wants/$u" ] || exit 1; done ;;
enable)
	for u in $units; do
		loaded "$u" || { echo "Failed to enable unit: Unit file $u does not exist." >&2; exit 1; }
		mkdir -p "$wants"
		ln -sf "$unitdir/$u" "$wants/$u"
	done ;;
disable)
	for u in $units; do
		rm -f "$wants/$u"
		[ "$now" = 1 ] && stop "$u"
	done ;;
start | restart)
	for u in $units; do
		loaded "$u" || { echo "Failed to $cmd $u: Unit not found." >&2; exit 5; }
		if [ -e "$state/fail/$u" ]; then
			stop "$u"
			echo "Job for $u failed." >&2
			exit 1
		fi
		start "$u"
	done ;;
stop)
	for u in $units; do
		loaded "$u" || { echo "Failed to stop $u: Unit not loaded." >&2; exit 5; }
		stop "$u"
	done ;;
*)
	echo "fake systemctl: $cmd isn't modeled" >&2
	exit 64 ;;
esac
exit 0
EOF
for tool in sysctl modprobe; do
	printf '#!/bin/sh\nexit 0\n' >"$work/bin/$tool"
done
# No waiting: a test that needs the daemon's socket has it already, and one that doesn't would
# otherwise wait the full 10 seconds for it. The calls say whether the scripts waited.
cat >"$work/bin/sleep" <<'EOF'
#!/bin/sh
echo "sleep $*" >>"$FAKE_SYSTEMCTL_STATE/calls"
EOF
chmod +x "$work/bin/"*

# ---- the packages ----

# build VERSION: the real scripts, units, and sysusers file, with stand-ins for the binary and
# for accept-ra (which reads and writes the host's network settings).
build() {
	local dir=$work/pkg-$1
	mkdir -p "$dir/DEBIAN" "$dir/usr/bin" "$dir/usr/lib/drawbridge" "$dir/usr/lib/systemd/system" \
		"$dir/usr/lib/sysusers.d"
	cat >"$dir/DEBIAN/control" <<EOF
Package: drawbridge
Version: $1
Architecture: all
Maintainer: test <test@example.com>
Description: test stand-in for drawbridge, with its real maintainer scripts
EOF
	cp "$root"/packaging/deb/{postinst,prerm,postrm} "$dir/DEBIAN/"
	cp "$root"/packaging/systemd/*.service "$dir/usr/lib/systemd/system/"
	cp "$root/packaging/sysusers/drawbridge.conf" "$dir/usr/lib/sysusers.d/"
	cat >"$dir/usr/bin/drawbridge" <<'EOF'
#!/bin/sh
# `admin setup-token` answers while the host has no admin account, and the daemon is up.
if [ "$1 $2" = "admin setup-token" ] && [ -S /run/drawbridge/control.sock ] &&
	[ -e "$FAKE_SYSTEMCTL_STATE/no-admin" ]; then
	echo "setup token: abc123"
	exit 0
fi
exit 1
EOF
	printf '#!/bin/sh\nexit 0\n' >"$dir/usr/lib/drawbridge/accept-ra"
	chmod 0755 "$dir/DEBIAN" "$dir/DEBIAN"/* "$dir/usr/bin/drawbridge" "$dir/usr/lib/drawbridge/accept-ra"
	dpkg-deb --root-owner-group -Zgzip --build "$dir" "$work/drawbridge_$1.deb" >/dev/null
}
build 1.0.0
build 1.1.0

# ---- the steps, and what to expect of them ----

failures=0
out= rc=

# Each dpkg step starts the call log afresh, so what a test sees is that step's calls only.
dpkg_step() {
	: >"$state/calls"
	out=$(dpkg "$@" 2>&1)
	rc=$?
}
install_pkg() { dpkg_step -i "$work/drawbridge_$1.deb"; }
remove_pkg() { dpkg_step -r drawbridge; }
purge_pkg() { dpkg_step -P drawbridge; }

# What an admin does by hand.
admin() { systemctl "$@" >/dev/null 2>&1; }

reset() {
	dpkg -P drawbridge >/dev/null 2>&1
	rm -rf /etc/drawbridge /var/lib/drawbridge /run/drawbridge "$state"
	rm -f "$wants/drawbridge.service" "$wants/drawbridge-tunnel.service"
	mkdir -p "$state/active" "$state/fail"
	touch "$state/no-admin"
	: >"$state/calls"
}

tunnel=drawbridge-tunnel.service daemon=drawbridge.service

enabled() { [ -e "$wants/$1" ]; }
active() { [ -e "$state/active/$1" ]; }
called() { grep -qxF -- "$1" "$state/calls"; }
called_like() { grep -qE -- "$1" "$state/calls"; }
user_exists() { getent passwd drawbridge >/dev/null; }
configured() { [ "$(dpkg-query -W -f='${db:Status-Abbrev}' drawbridge 2>/dev/null)" = "ii " ]; }
said() { grep -qF -- "$1" <<<"$out"; }
stopped_daemon_first() {
	local d t
	d=$(grep -nxF "stop $daemon" "$state/calls" | cut -d: -f1)
	t=$(grep -nxF "stop $tunnel" "$state/calls" | cut -d: -f1)
	[ -n "$d" ] && [ -n "$t" ] && [ "$d" -lt "$t" ]
}

expect() {
	local what=$1
	shift
	"$@" || { echo "  FAIL: $what"; failures=$((failures + 1)); }
}
expect_not() {
	local what=$1
	shift
	if "$@"; then echo "  FAIL: $what"; failures=$((failures + 1)); fi
}

run() {
	local name=$1 before=$failures
	shift
	echo "=== $name"
	reset
	"$@"
	if [ "$failures" -gt "$before" ]; then
		echo "--- the last dpkg step's output:"
		echo "$out"
		echo "--- its systemctl and sleep calls:"
		cat "$state/calls"
	fi
}

# ---- first install ----

first_install() {
	install_pkg 1.0.0
	expect "dpkg succeeds" [ "$rc" = 0 ]
	expect "the package is configured" configured
	expect "the units are enabled together" called "enable $tunnel $daemon"
	expect "the tunnel is enabled" enabled $tunnel
	expect "the daemon is enabled" enabled $daemon
	expect "the tunnel is started" active $tunnel
	expect "the daemon is started" active $daemon
	expect "systemd reads the new unit files" called "daemon-reload"
	expect "the setup token is shown" said "setup token: abc123"
	expect_not "the tunnel isn't restarted" called "restart $tunnel"
}

first_install_daemon_fails() {
	touch "$state/fail/$daemon"
	install_pkg 1.0.0
	expect "dpkg succeeds" [ "$rc" = 0 ]
	expect "the package is configured, not half-configured" configured
	expect "the message says where to look" said "journalctl -u drawbridge"
	expect "the tunnel is up" active $tunnel
	expect "the daemon is enabled anyway" enabled $daemon
	expect_not "the daemon isn't running" active $daemon
	expect_not "nothing waits for a socket that won't come" called_like '^sleep'
	expect_not "no setup token is shown" said "setup token"
}

# ---- upgrades and downgrades ----

upgrade_both_running() {
	install_pkg 1.0.0
	install_pkg 1.1.0
	expect "dpkg succeeds" [ "$rc" = 0 ]
	expect "the package is configured" configured
	expect "the daemon is restarted" called "restart $daemon"
	expect_not "the tunnel isn't restarted" called "restart $tunnel"
	expect_not "the tunnel isn't stopped" called "stop $tunnel"
	expect_not "nothing is enabled again" called_like '^enable'
	expect_not "nothing is disabled" called_like '^disable'
	expect "the tunnel is still up" active $tunnel
	expect "the daemon is up" active $daemon
	expect "both are still enabled" enabled $tunnel
	expect "both are still enabled" enabled $daemon
}

upgrade_stopped_tunnel() {
	install_pkg 1.0.0
	admin stop $tunnel
	install_pkg 1.1.0
	expect "dpkg succeeds" [ "$rc" = 0 ]
	expect "an enabled tunnel is started" active $tunnel
	expect_not "and not restarted" called "restart $tunnel"
}

upgrade_daemon_disabled() {
	install_pkg 1.0.0
	admin disable $daemon
	[ "$1" = stopped ] && admin stop $daemon
	install_pkg 1.1.0
	expect "dpkg succeeds" [ "$rc" = 0 ]
	expect "the package is configured" configured
	expect_not "the daemon stays disabled" enabled $daemon
	expect_not "the daemon isn't started or restarted" called_like "^(re)?start $daemon"
	expect_not "the daemon isn't enabled" called_like "^enable.*$daemon"
	if [ "$1" = stopped ]; then
		expect_not "a stopped daemon stays stopped" active $daemon
	else
		expect "a running daemon is left running" active $daemon
	fi
	expect_not "nothing waits for its socket" called_like '^sleep'
	expect_not "no setup token is shown" said "setup token"
	expect "the tunnel is still enabled" enabled $tunnel
	expect "the tunnel is still up" active $tunnel
}

upgrade_tunnel_disabled() {
	install_pkg 1.0.0
	admin disable $tunnel
	[ "$1" = stopped ] && admin stop $tunnel
	install_pkg 1.1.0
	expect "dpkg succeeds" [ "$rc" = 0 ]
	expect_not "the tunnel stays disabled" enabled $tunnel
	expect_not "the tunnel isn't touched" called_like "^(enable|disable|start|restart|stop) .*$tunnel"
	if [ "$1" = stopped ]; then
		expect_not "a stopped tunnel stays stopped" active $tunnel
	else
		expect "a running tunnel is left running" active $tunnel
	fi
	expect "the daemon is still enabled" enabled $daemon
	expect "the daemon is restarted" called "restart $daemon"
}

upgrade_both_disabled() {
	install_pkg 1.0.0
	admin disable $tunnel $daemon
	install_pkg 1.1.0
	expect "dpkg succeeds" [ "$rc" = 0 ]
	expect_not "neither is enabled" enabled $tunnel
	expect_not "neither is enabled" enabled $daemon
	expect_not "nothing is enabled, started, restarted, stopped, or disabled" \
		called_like '^(enable|disable|start|restart|stop)'
	expect_not "nothing waits for the socket" called_like '^sleep'
}

upgrade_daemon_fails() {
	install_pkg 1.0.0
	touch "$state/fail/$daemon"
	install_pkg 1.1.0
	expect "dpkg succeeds" [ "$rc" = 0 ]
	expect "the package is configured, not half-configured" configured
	expect "the message says where to look" said "journalctl -u drawbridge"
	expect "the tunnel is still up" active $tunnel
	expect "the daemon is still enabled" enabled $daemon
}

downgrade_daemon_disabled() {
	install_pkg 1.1.0
	admin disable $daemon
	install_pkg 1.0.0
	expect "dpkg succeeds" [ "$rc" = 0 ]
	expect "the version is the older one" [ "$(dpkg-query -W -f='${Version}' drawbridge)" = 1.0.0 ]
	expect_not "the daemon stays disabled" enabled $daemon
	expect_not "the daemon isn't started" called_like "^(re)?start $daemon"
	expect "the tunnel is up" active $tunnel
}

downgrade_daemon_fails() {
	install_pkg 1.1.0
	# What the daemon does to a database from a newer build: exit 78 (docs/PLAN.md §11).
	touch "$state/fail/$daemon"
	install_pkg 1.0.0
	expect "dpkg succeeds" [ "$rc" = 0 ]
	expect "the package is configured, not half-configured" configured
	expect "the message says where to look" said "journalctl -u drawbridge"
	expect "the tunnel is still up" active $tunnel
}

aborted_upgrade() {
	install_pkg 1.0.0
	admin disable $tunnel $daemon
	: >"$state/calls"
	out=$(/var/lib/dpkg/info/drawbridge.postinst abort-upgrade 1.1.0 2>&1)
	rc=$?
	expect "the script succeeds" [ "$rc" = 0 ]
	expect_not "nothing is enabled, started, restarted, stopped, or disabled" \
		called_like '^(enable|disable|start|restart|stop)'
	expect_not "the tunnel stays disabled" enabled $tunnel
	expect_not "the daemon stays disabled" enabled $daemon
}

# ---- remove and purge ----

remove() {
	install_pkg 1.0.0
	remove_pkg
	expect "dpkg succeeds" [ "$rc" = 0 ]
	expect "the daemon is stopped" called "stop $daemon"
	expect "the tunnel is stopped" called "stop $tunnel"
	expect "the daemon stops first" stopped_daemon_first
	expect_not "the units aren't disabled" called_like '^disable'
	expect_not "the tunnel is down" active $tunnel
	expect_not "the daemon is down" active $daemon
	expect "the units' links stay, so a reinstall finds them enabled" [ -L "$wants/$tunnel" ]
	expect "the units' links stay, so a reinstall finds them enabled" [ -L "$wants/$daemon" ]
	expect "the data stays" [ -s /etc/drawbridge/secret.key ]
}

reinstall_after_remove() {
	install_pkg 1.0.0
	remove_pkg
	install_pkg 1.0.0
	expect "dpkg succeeds" [ "$rc" = 0 ]
	expect "the package is configured" configured
	expect "the tunnel is enabled" enabled $tunnel
	expect "the daemon is enabled" enabled $daemon
	expect "the tunnel is up" active $tunnel
	expect "the daemon is up" active $daemon
	expect "the key is still there" [ -s /etc/drawbridge/secret.key ]
}

reinstall_after_remove_daemon_disabled() {
	install_pkg 1.0.0
	admin disable $daemon
	remove_pkg
	install_pkg 1.0.0
	expect "dpkg succeeds" [ "$rc" = 0 ]
	expect_not "the daemon stays disabled" enabled $daemon
	expect_not "the daemon isn't started" active $daemon
	expect "the tunnel is enabled" enabled $tunnel
	expect "the tunnel is up" active $tunnel
}

purge() {
	install_pkg 1.0.0
	mkdir -p /var/lib/drawbridge
	touch /var/lib/drawbridge/db.sqlite
	remove_pkg
	purge_pkg
	expect "dpkg succeeds" [ "$rc" = 0 ]
	expect_not "the daemon's link is gone" test -e "$wants/$daemon" -o -L "$wants/$daemon"
	expect_not "the tunnel's link is gone" test -e "$wants/$tunnel" -o -L "$wants/$tunnel"
	expect_not "the configuration is gone" test -e /etc/drawbridge
	expect_not "the data is gone" test -e /var/lib/drawbridge
	expect "the user stays" user_exists
}

purge_directly() {
	install_pkg 1.0.0
	purge_pkg
	expect "dpkg succeeds" [ "$rc" = 0 ]
	expect_not "the daemon's link is gone" test -e "$wants/$daemon" -o -L "$wants/$daemon"
	expect_not "the tunnel's link is gone" test -e "$wants/$tunnel" -o -L "$wants/$tunnel"
}

install_after_purge() {
	install_pkg 1.0.0
	remove_pkg
	purge_pkg
	touch "$state/no-admin"
	install_pkg 1.0.0
	expect "dpkg succeeds" [ "$rc" = 0 ]
	expect "the units are enabled, as on a first install" called "enable $tunnel $daemon"
	expect "the tunnel is up" active $tunnel
	expect "the daemon is up" active $daemon
	expect "the setup token is shown" said "setup token: abc123"
}

install_after_purge_disabled() {
	install_pkg 1.0.0
	admin disable $tunnel $daemon
	purge_pkg
	install_pkg 1.0.0
	expect "dpkg succeeds" [ "$rc" = 0 ]
	expect "a first install enables what was disabled before the purge" enabled $tunnel
	expect "a first install enables what was disabled before the purge" enabled $daemon
	expect "the tunnel is up" active $tunnel
	expect "the daemon is up" active $daemon
}

run "a first install enables both units, starts them, and shows the setup token" first_install
run "a first install whose daemon won't start still configures the package" first_install_daemon_fails
run "an upgrade restarts the daemon, and leaves the tunnel and the enabled state alone" upgrade_both_running
run "an upgrade starts an enabled tunnel that had stopped" upgrade_stopped_tunnel
run "an upgrade keeps a stopped daemon the admin disabled off" upgrade_daemon_disabled stopped
run "an upgrade keeps a running daemon the admin disabled off, and running" upgrade_daemon_disabled running
run "an upgrade keeps a stopped tunnel the admin disabled off" upgrade_tunnel_disabled stopped
run "an upgrade leaves a running tunnel the admin disabled running" upgrade_tunnel_disabled running
run "an upgrade over two disabled units does nothing to them" upgrade_both_disabled
run "an upgrade whose daemon won't restart still configures the package" upgrade_daemon_fails
run "a downgrade keeps a disabled daemon off" downgrade_daemon_disabled
run "a downgrade whose daemon refuses the newer database still configures the package" downgrade_daemon_fails
run "an aborted upgrade starts nothing" aborted_upgrade
run "remove stops both units and leaves them enabled" remove
run "a reinstall after remove brings both back" reinstall_after_remove
run "a reinstall after remove keeps a disabled daemon off" reinstall_after_remove_daemon_disabled
run "purge, after remove, deletes the unit links and the data" purge
run "purge without a remove first does the same" purge_directly
run "an install after purge is a first install" install_after_purge
run "an install after purge enables units that were disabled" install_after_purge_disabled

echo
if [ "$failures" -gt 0 ]; then
	echo "$failures check(s) failed" >&2
	exit 1
fi
echo "every check passed"
