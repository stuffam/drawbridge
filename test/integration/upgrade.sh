#!/usr/bin/env bash
# Runs TestUpgradeFromAnOlderBuild once for each older build listed in upgrade-from.txt, or for
# the git refs given as arguments: it builds the older binary from the ref, and the test sets up
# a host with it and upgrades the host to the binary in DRAWBRIDGE_BIN (docs/PLAN.md §12).
# `make test-upgrade` runs it; CI runs it after the other kernel tests.
#
# It needs what `make test-integration` needs (root or passwordless sudo, ip and nft, IPv6, the
# wireguard module), a full clone so that the refs exist, and Go. Every ref is tried, and the
# exit status is 1 if any failed.
set -uo pipefail

cd "$(dirname "$0")/../.."
root=$PWD
: "${DRAWBRIDGE_BIN:=$root/dist/drawbridge}"
export DRAWBRIDGE_BIN

refs=("$@")
if [ ${#refs[@]} -eq 0 ]; then
	while read -r ref; do
		refs+=("$ref")
	done < <(sed -e 's/#.*//' -e 's/[[:space:]]*$//' test/integration/upgrade-from.txt | grep .)
fi
[ -x "$DRAWBRIDGE_BIN" ] || { echo "no binary at $DRAWBRIDGE_BIN; run make build first" >&2; exit 1; }

exec_args=()
if [ "$(id -u)" != 0 ]; then exec_args=(-exec "sudo -E"); fi

work=$(mktemp -d)
# Removes only the checkouts this script made. Never `git worktree prune`: it deletes the
# registration of every worktree whose directory this machine can't see, which in a container
# with the repository mounted is every other worktree the repository has.
cleanup() {
	for src in "$work"/src-*; do
		[ -e "$src" ] && git worktree remove --force "$src" 2>/dev/null
	done
	rm -rf "$work"
}
trap cleanup EXIT

failed=()
for ref in "${refs[@]}"; do
	echo
	echo "=== upgrading from $ref ($(git log -1 --format=%s "$ref" 2>/dev/null))"
	src=$work/src-$ref
	if ! git worktree add --detach "$src" "$ref" >/dev/null; then
		echo "can't check out $ref; the clone needs the whole history (git fetch --unshallow)" >&2
		failed+=("$ref")
		continue
	fi
	# Built where the older tree is, so its own go.mod and dependencies apply.
	if ! (cd "$src" && CGO_ENABLED=0 go build -o "$work/drawbridge-$ref" ./cmd/drawbridge); then
		failed+=("$ref")
		continue
	fi
	git worktree remove --force "$src"
	if ! DRAWBRIDGE_INTEGRATION=1 DRAWBRIDGE_OLD_BIN="$work/drawbridge-$ref" \
		go test -tags integration -count=1 -v -run TestUpgradeFromAnOlderBuild "${exec_args[@]}" ./test/integration/; then
		failed+=("$ref")
	fi
done

echo
if [ ${#failed[@]} -gt 0 ]; then
	echo "the upgrade from these failed: ${failed[*]}" >&2
	exit 1
fi
echo "upgraded from every build in the list, ${#refs[@]} in all"
