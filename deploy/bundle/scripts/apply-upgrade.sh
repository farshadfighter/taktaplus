#!/bin/sh
# Applies a verified, extracted bundle. Run by upgrade.sh and by the updater
# container:  apply-upgrade.sh ID STAGED_DIR VERSION
# Steps: database snapshot and encrypted backup, load images, switch the
# compose file and version, start, health check; on failure, the previous
# version and the database snapshot come back.
set -u
cd "$(dirname "$0")/.."
. scripts/product.sh
. scripts/common.sh
ID=$1 STAGED=$2 NEW=$3
U=data/upgrade
STEP=start
OLD=$(get_env "${ENV_PREFIX}_VERSION")

status() {
	printf '{"id":%s,"state":"%s","step":"%s","message":"%s"}\n' "$ID" "$1" "$2" "$3" > "$U/status.json.tmp"
	cp "$U/status.json.tmp" "$U/status-$ID.json.tmp" && mv "$U/status-$ID.json.tmp" "$U/status-$ID.json"
	mv "$U/status.json.tmp" "$U/status.json"
	echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) [$1/$2] $3"
}
fail() { status failed "$STEP" "$1"; exit 1; }

rollback() {
	STEP=rollback
	status running rollback "$1؛ بازگشت به $OLD"
	[ -f .env.prev ] && mv .env.prev .env
	[ -f docker-compose.yml.prev ] && mv docker-compose.yml.prev docker-compose.yml
	if [ -d scripts.prev ]; then
		for f in scripts.prev/*.sh; do install_file "$f" "scripts/$(basename "$f")" 755; done
	fi
	docker compose stop "$SVC"
	docker compose run --rm -T "$SVC" upgrade-rollback || { status failed rollback "بازگرداندن نسخه‌ی پشتیبان دیتابیس ناموفق بود؛ با scripts/restore.sh یک بکاپ را بازگردانید"; exit 1; }
	docker compose up -d $(services)
	if wait_healthy 60; then
		status rolled_back rollback "$1؛ نسخه‌ی $OLD دوباره در حال اجراست"
	else
		status failed rollback "$1؛ نسخه‌ی $OLD هم بالا نیامد"
	fi
	exit 1
}

[ -d "$STAGED" ] || fail "پوشه‌ی بسته پیدا نشد"
status running snapshot "ارتقا از $OLD به $NEW: نسخه‌ی پشتیبان دیتابیس"
STEP=snapshot
api_running || docker compose up -d $(services)
wait_healthy 24 || fail "نسخه‌ی فعلی سالم نیست؛ پیش از ارتقا آن را رفع کنید"
docker compose exec -T "$SVC" "$BIN" upgrade-snapshot || fail "نسخه‌ی پشتیبان دیتابیس ناموفق بود"
STEP=backup
status running backup "بکاپ رمزنگاری‌شده"
docker compose exec -T "$SVC" "$BIN" system-backup -trigger pre_upgrade || echo "هشدار: بکاپ رمزنگاری‌شده گرفته نشد (عبارت عبور بکاپ را در پنل تعیین کنید)"

STEP=load
status running load "بارگذاری ایمیج‌ها"
for f in "$STAGED"/images/*.tar; do
	[ -f "$f" ] || continue
	docker load -i "$f" || fail "بارگذاری $(basename "$f") ناموفق بود"
done

STEP=switch
status running switch "تعویض به نسخه‌ی $NEW"
cp -p .env .env.prev
cp -p docker-compose.yml docker-compose.yml.prev
rm -rf scripts.prev && mkdir scripts.prev && cp -p scripts/*.sh scripts.prev/
install_file "$STAGED/docker-compose.yml" docker-compose.yml
for f in "$STAGED"/scripts/*.sh; do install_file "$f" "scripts/$(basename "$f")" 755; done
set_env "${ENV_PREFIX}_VERSION" "$NEW"

STEP=start
status running start "راه‌اندازی $NEW"
docker compose up -d $(services) || rollback "راه‌اندازی $NEW ناموفق بود"
STEP=health
status running health "انتظار برای سالم بالا آمدن $NEW"
wait_healthy 60 || rollback "نسخه‌ی $NEW سالم بالا نیامد"

rm -rf "$STAGED" .env.prev docker-compose.yml.prev scripts.prev "$U/rollback.dump"
status done done "ارتقا از $OLD به $NEW انجام شد"
exit 0
