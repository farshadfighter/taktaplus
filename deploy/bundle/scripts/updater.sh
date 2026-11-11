#!/bin/sh
# Runs in the updater container: applies upgrades queued from the panel.
cd "$(dirname "$0")/.."
U=data/upgrade
while :; do
	# The panel (uid 65532) writes staged bundles here.
	[ -d "$U" ] || { mkdir -p "$U" && chown 65532:65532 "$U"; }
	touch "$U/updater.alive"
	if [ -f "$U/request.json" ]; then
		req=$(cat "$U/request.json")
		id=$(echo "$req" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
		ver=$(echo "$req" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')
		dir=$(echo "$req" | sed -n 's/.*"dir":"\([^"]*\)".*/\1/p')
		mv "$U/request.json" "$U/request.taken"
		case "$dir" in
		upgrade/staged-[0-9]*) sh scripts/apply-upgrade.sh "$id" "data/$dir" "$ver" >> "$U/upgrade-$id.log" 2>&1 ;;
		*) echo "rejected request: $req" >> "$U/updater.log" ;;
		esac
		rm -f "$U/request.taken"
	fi
	sleep 10
done
