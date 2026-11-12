#!/bin/sh
# Builds the signed offline bundle dist/taktaplus-VERSION.tar.gz.
#   scripts/release/build-bundle.sh VERSION RELEASE_KEY [MIN_FROM]
# RELEASE_KEY is an encrypted key from `lictool keygen` whose public key is
# in internal/system/keys/ (and so built into this version); the key
# passphrase is read from $TPLIC_KEY_PASSPHRASE. LICTOOL names the lictool
# binary (default: lictool on PATH).
set -eu
VERSION=$1 KEY=$2 MIN_FROM=${3:-}
LICTOOL=${LICTOOL:-lictool}
cd "$(dirname "$0")/../.."
ls internal/system/keys/*.pub >/dev/null 2>&1 || { echo "no release public key in internal/system/keys/" >&2; exit 1; }

IMAGES="taktaplus:$VERSION postgres:16-alpine clickhouse/clickhouse-server:24.8-alpine docker:27-cli"
docker build --build-arg VERSION="$VERSION" --build-arg COMMIT="$(git rev-parse --short HEAD)" \
	--build-arg DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" -t "taktaplus:$VERSION" .
for i in $IMAGES; do
	case "$i" in taktaplus:*) ;; *) docker image inspect "$i" >/dev/null 2>&1 || docker pull "$i" ;; esac
done

OUT=$(mktemp -d)
trap 'rm -rf "$OUT"' EXIT
mkdir -p "$OUT/images" "$OUT/scripts" dist
# shellcheck disable=SC2086
docker save -o "$OUT/images/images.tar" $IMAGES
chmod 644 "$OUT/images/images.tar"
cp deploy/bundle/docker-compose.yml deploy/bundle/install.sh "$OUT/"
cp deploy/bundle/scripts/*.sh "$OUT/scripts/"
chmod 755 "$OUT/install.sh" "$OUT"/scripts/*.sh
"$LICTOOL" build-release -key "$KEY" -dir "$OUT" -product taktaplus -version "$VERSION" -min-from "$MIN_FROM" \
	-images "$(echo $IMAGES | tr ' ' ,)" -notes "${NOTES:-}" -out "dist/taktaplus-$VERSION.tar.gz"
