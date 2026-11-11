# Shared helpers for the TaktaPlus scripts; sourced, not run.
# The working directory is the installation directory ($TAKTAPLUS_HOME).

die() { echo "error: $*" >&2; exit 1; }

# set_env NAME VALUE: replace or append NAME=VALUE in .env.
set_env() {
	tmp=$(mktemp .env.XXXXXX)
	grep -v "^$1=" .env > "$tmp" || true
	printf '%s=%s\n' "$1" "$2" >> "$tmp"
	chmod 600 "$tmp"
	mv "$tmp" .env
}

get_env() { sed -n "s/^$1=//p" .env | tail -n 1; }

# services: every service except the updater, which must not restart itself.
services() { docker compose config --services | grep -vx updater; }

api_running() { [ -n "$(docker compose ps --status running -q api 2>/dev/null)" ]; }

# wait_healthy [tries]: wait until the panel answers /healthz; give up
# early when the api container keeps crashing.
wait_healthy() {
	n=${1:-60}
	i=0
	down=0
	until docker compose exec -T api wget -q -O /dev/null --no-check-certificate https://127.0.0.1:8443/healthz 2>/dev/null; do
		i=$((i + 1))
		[ "$i" -ge "$n" ] && return 1
		case "$(docker compose ps -a --format '{{.State}}' api 2>/dev/null)" in
		restarting | exited | dead) down=$((down + 1)) ;;
		*) down=0 ;;
		esac
		[ "$down" -ge 6 ] && return 1
		sleep 5
	done
}

# install_file SRC DST: replace DST with a new inode, so a shell that is
# running the old file keeps reading it.
install_file() {
	cp "$1" "$2.new" && chmod "${3:-644}" "$2.new" && mv "$2.new" "$2"
}
