#!/bin/sh
# Takes an encrypted backup now (the passphrase must be set in the panel):
#   sudo /opt/taktaplus/scripts/backup.sh
# The file is written to data/system-backups/.
set -eu
cd "$(dirname "$0")/.."
. scripts/common.sh
api_running || die "TaktaPlus is not running"
docker compose exec -T api taktaplus system-backup | sed 's|/var/lib/taktaplus/|'"$(pwd)"'/data/|'
