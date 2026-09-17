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

isnum() { case "$1" in ''|*[!0-9]*) return 1 ;; *) return 0 ;; esac; }

# valida HH:MM (valor ruim = fallback, nunca loop quente no sleep)
if ! isnum "$H" || ! isnum "$M" || [ "$H" -gt 23 ] || [ "$M" -gt 59 ]; then
	echo "[autosync] AUTOSYNC_HOUR invalida ($HOUR), usando 04:00" >&2
	HOUR="04:00"
	H=04
	M=00
fi

# limpa lock obsoleto de shutdown sujo (senao o mkdir abaixo falha para sempre
# e o sync diario morre em silencio); rmdir e idempotente em todo o script.
# O 'exit 143' no trap de sinal e obrigatorio: sem ele o shell engole o
# SIGTERM (handler sem saida) e 'docker stop' trava ate o SIGKILL.
rmdir "$LOCK" 2>/dev/null
trap 'rmdir "$LOCK" 2>/dev/null' EXIT
trap 'rmdir "$LOCK" 2>/dev/null; exit 143' INT TERM

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"; }

# sleep interrompivel por sinal: com trap armado, o dash so roda o handler
# quando um 'sleep' em foreground termina (sinal fica pendente por horas);
# com 'sleep & wait' o sinal interrompe na hora e o handler sai (exit 143)
wsleep() { sleep "$1" & wait "$!"; }

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
	wsleep $((target - $(date +%s)))

	if ! mkdir "$LOCK" 2>/dev/null; then
		log "[autosync] outro autosync em andamento, adiando 10min"
		wsleep 600
		continue
	fi

	# download em andamento? (running=1 E heartbeat recente; um run ja
	# concluido nao bloqueia o sync — so adianta se houver atividade real)
	if [ -f "$STATUS" ]; then
		upd=$(sed -n 's/.*"updated_at": *\([0-9][0-9]*\).*/\1/p' "$STATUS" | tail -n1)
		run=$(sed -n 's/.*"running": *\([01]\).*/\1/p' "$STATUS" | tail -n1)
		age=$(($(date +%s) - ${upd:-0}))
		if [ "${run:-0}" = "1" ] && [ "$age" -lt 900 ]; then
			log "[autosync] download em andamento (heartbeat ${age}s), adiando 30min"
			rmdir "$LOCK" 2>/dev/null
			wsleep 1800
			continue
		fi
	fi

	log "[autosync] iniciando sync incremental"
	SYNC_MODE=incremental /app/entrypoint.sh
	rc=$?
	log "[autosync] sync terminou rc=$rc"

	rmdir "$LOCK" 2>/dev/null
done
