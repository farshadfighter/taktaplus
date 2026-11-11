#!/bin/sh
# Takes an encrypted backup now (the passphrase must be set in the panel):
#   sudo <installation>/scripts/backup.sh
# The file is written to data/system-backups/.
set -eu
cd "$(dirname "$0")/.."
. scripts/product.sh
. scripts/common.sh
api_running || die "$PRODUCT is not running"
docker compose exec -T "$SVC" "$BIN" system-backup | sed "s|$CDATA/|$(pwd)/data/|"
