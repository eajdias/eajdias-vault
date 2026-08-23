#!/bin/sh
# Sync incremental diario: dorme ate AUTOSYNC_HOUR, dispara o entrypoint do
# downloader (mesma imagem/config) e volta a dormir. Protecoes:
#   - lock por mkdir em /data/db (nao corre duas vezes nem com restart)
#   - ignora se um download manual/UI estiver ativo (heartbeat recente no
#     .download-status.json) e tenta de novo mais tarde
set -u

HOUR="${AUTOSYNC_HOUR:-04:00}"
H="${HOUR%:*}"
M="${HOUR#*:}"
LOCK="/data/db/.autosync-lock"
STATUS="/data/media/.download-status.json"

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"; }

now_target() {
	python3 - "$H" "$M" <<'PY'
import sys, datetime
h, m = int(sys.argv[1]), int(sys.argv[2])
t = datetime.datetime.combine(datetime.date.today(), datetime.time(h, m))
if t.timestamp() <= datetime.datetime.now().timestamp():
    t += datetime.timedelta(days=1)
print(int(t.timestamp()))
PY
}

log "[autosync] proxima execucao as $HOUR"

while :; do
	target=$(now_target)
	sleep $((target - $(date +%s)))

	if ! mkdir "$LOCK" 2>/dev/null; then
		log "[autosync] outro autosync em andamento, adiando 10min"
		sleep 600
		continue
	fi
	trap 'rmdir "$LOCK" 2>/dev/null' INT TERM

	# download ativo? (heartbeat com menos de 15 min)
	if [ -f "$STATUS" ]; then
		upd=$(sed -n 's/.*"updated_at": *\([0-9][0-9]*\).*/\1/p' "$STATUS" | tail -n1)
		age=$(($(date +%s) - ${upd:-0}))
		if [ "$age" -lt 900 ]; then
			log "[autosync] download ja ativo (heartbeat ${age}s), adiando 30min"
			rmdir "$LOCK" 2>/dev/null
			trap - INT TERM
			sleep 1800
			continue
		fi
	fi

	log "[autosync] iniciando sync incremental"
	SYNC_MODE=incremental /app/entrypoint.sh
	rc=$?
	log "[autosync] sync terminou rc=$rc"

	rmdir "$LOCK" 2>/dev/null
	trap - INT TERM
done
