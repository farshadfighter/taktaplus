#!/bin/sh
# Upgrades from an offline bundle, on the server:
#   sudo <installation>/scripts/upgrade.sh <product>-x.y.z.tar.gz
# The running version verifies the bundle's signature first.
set -eu
B=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")
cd "$(dirname "$0")/.."
. scripts/product.sh
. scripts/common.sh
[ -f .env ] || die "run from an installation (no .env)"
[ -f "$B" ] || die "$B not found"

echo "==> verifying $B"
if api_running; then
	out=$(docker compose exec -T "$SVC" "$BIN" stage-upgrade -no-queue < "$B") || die "verification failed"
else
	out=$(docker compose run --rm -T "$SVC" stage-upgrade -no-queue < "$B") || die "verification failed"
fi
set -- $out
ID=$1 VERSION=$2 DIR=$3
sh scripts/apply-upgrade.sh "$ID" "data/$DIR" "$VERSION" 2>&1 | tee -a "data/upgrade/upgrade-$ID.log"
grep -q '"state":"done"' "data/upgrade/status-$ID.json"
