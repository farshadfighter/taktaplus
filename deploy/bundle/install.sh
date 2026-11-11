#!/bin/sh
# Installs TaktaPlus from this extracted offline bundle.
#   sudo ./install.sh [FILESERVER_IP]
# FILESERVER_IP is the address devices use to reach this server (TFTP/FTP,
# syslog). TAKTAPLUS_HOME chooses the installation directory.
set -eu
SRC=$(cd "$(dirname "$0")" && pwd)
HOME_DIR=${TAKTAPLUS_HOME:-/opt/taktaplus}
. "$SRC/scripts/common.sh"

[ "$(id -u)" = 0 ] || die "run as root"
command -v docker >/dev/null || die "Docker Engine is not installed"
docker compose version >/dev/null 2>&1 || die "the Docker Compose plugin is not installed"
[ -f "$HOME_DIR/.env" ] && die "TaktaPlus is already installed in $HOME_DIR; use $HOME_DIR/scripts/upgrade.sh"
VERSION=$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' "$SRC/manifest.json" | head -n 1)
[ -n "$VERSION" ] || die "manifest.json not found: run install.sh from the extracted bundle"

echo "==> loading images"
for f in "$SRC"/images/*.tar; do docker load -i "$f"; done
echo "==> verifying the bundle"
docker run --rm --network none --user 0:0 --entrypoint /usr/local/bin/taktaplus -v "$SRC:/bundle:ro" "taktaplus:$VERSION" verify-bundle-dir /bundle \
	|| die "the bundle failed verification"

IP=${1:-}
if [ -z "$IP" ]; then
	printf 'address devices use to reach this server: '
	read -r IP
fi
[ -n "$IP" ] || die "the server address is required"

echo "==> installing into $HOME_DIR"
mkdir -p "$HOME_DIR/scripts" "$HOME_DIR/data"
cp "$SRC/docker-compose.yml" "$HOME_DIR/"
cp "$SRC"/scripts/*.sh "$HOME_DIR/scripts/"
chmod 755 "$HOME_DIR"/scripts/*.sh
cd "$HOME_DIR"
rand() { head -c "$1" /dev/urandom | base64 | tr -d '/+=\n' | head -c "$2"; }
umask 077
cat > .env <<ENV
TAKTAPLUS_HOME=$HOME_DIR
TAKTAPLUS_VERSION=$VERSION
TAKTAPLUS_MASTER_KEY=$(head -c 32 /dev/urandom | base64)
TAKTAPLUS_DB_PASSWORD=$(rand 32 32)
TAKTAPLUS_CH_PASSWORD=$(rand 32 32)
TAKTAPLUS_FILESERVER_IP=$IP
TAKTAPLUS_LICENSE_SERVER=
TAKTAPLUS_TIMEZONE=Asia/Tehran
TAKTAPLUS_HTTPS_PORT=443
ENV
umask 022
# The panel runs as uid 65532.
chown 65532:65532 data
chmod 700 data

echo "==> starting"
docker compose up -d
wait_healthy 60 || die "TaktaPlus did not start; see: docker compose -f $HOME_DIR/docker-compose.yml logs api"
cat <<MSG

TaktaPlus $VERSION is running on https://$IP/
Create the first administrator:
  cd $HOME_DIR && docker compose exec api taktaplus create-admin -username admin
Keep $HOME_DIR/.env safe: it holds the master key. Set a backup passphrase in
the panel (System > Backup & upgrade) so backups can be restored elsewhere.
MSG
