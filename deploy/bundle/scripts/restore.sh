#!/bin/sh
# Restores a .tpbak backup over this installation (for example on a new
# server installed with the same or a newer version):
#   sudo <installation>/scripts/restore.sh FILE.tpbak
# It asks for the backup passphrase, replaces the database and data files
# and the master key in .env, and starts the service again.
set -eu
F=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")
cd "$(dirname "$0")/.."
. scripts/product.sh
. scripts/common.sh
[ -f "$F" ] || die "$F not found"
printf 'This replaces ALL data of this installation with the backup. Type "restore" to continue: '
read -r ok
[ "$ok" = restore ] || die "cancelled"
PASS=$(printenv "${ENV_PREFIX}_BACKUP_PASSPHRASE" || true)
if [ -z "$PASS" ]; then
	printf 'backup passphrase: '
	stty -echo 2>/dev/null || true
	read -r PASS
	stty echo 2>/dev/null || true
	echo
fi
[ -d data/upgrade ] || { mkdir -p data/upgrade && chown 65532:65532 data/upgrade; }
cp "$F" data/upgrade/restore.tpbak
chown 65532:65532 data/upgrade data/upgrade/restore.tpbak 2>/dev/null || true
trap 'rm -f data/upgrade/restore.tpbak data/upgrade/master.key' EXIT
docker compose stop "$SVC"
docker compose up -d postgres
printf '%s\n' "$PASS" | docker compose run --rm -T "$SVC" restore-backup \
	-file "$CDATA/upgrade/restore.tpbak" -master-key-out "$CDATA/upgrade/master.key"
if [ -s data/upgrade/master.key ]; then
	set_env "${ENV_PREFIX}_MASTER_KEY" "$(cat data/upgrade/master.key)"
fi
docker compose up -d $(services)
wait_healthy 60 || die "$PRODUCT did not start after the restore; see: docker compose logs $SVC"
echo "restored; $PRODUCT is running"
