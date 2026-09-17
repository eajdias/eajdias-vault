#!/bin/sh
# Sync incremental diario: dorme ate AUTOSYNC_HOUR, dispara o entrypoint do
# downloader (mesma imagem/config) e volta a dormir. Protecoes:
#   - lock por mkdir em /data/db (nao corre duas vezes nem com restart)
#   - ignora se um download manual/UI estiver ativo (heartbeat recente no
#     .download-status.json) e tenta de novo mais tarde
set -u

# opacidade: lock 750 + grupo do entrypoint (mesma regra de la)
umask 027

HOUR="${AUTOSYNC_HOUR:-04:00}"
H="${HOUR%:*}"
M="${HOUR#*:}"
LOCK="/data/db/.autosync-lock"
STATUS="/data/media/.download-status.json"
CTRL_DIR="/data/db/.uictrl"

# dir de controle da UI (ver entrypoint.sh): comandos start/stop + ack.
# o viewer escreve via grupo vault; execucao e sempre aqui (root).
mkdir -p "$CTRL_DIR"
chgrp 2000 "$CTRL_DIR" 2>/dev/null
chmod 2770 "$CTRL_DIR" 2>/dev/null

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

log "[autosync] proxima execucao as $HOUR (comandos da UI a cada 30s)"

# ---- controle via UI -------------------------------------------------------
# Protocolo em $CTRL_DIR (sem socket docker):
#   UI escreve cmd.json {"action":"start","mode":"full"|"incremental","id":N}
#   ou o arquivo stop (conteudo = id); executor responde ack.json e remove.
#   stop com sync ativo e consumido pelo sampler do entrypoint (ate 15s);
#   aqui o stop so e consumido se ocioso (cura flag orfa).
# poll_cmd retorna 0 SOMENTE quando executou um start (o loop reprograma o
# dia); nos demais casos retorna 1 e a espera continua.

ack() { # $1 id $2 state [$3 detail]
	if [ -n "${3:-}" ]; then extra=",\"detail\":\"$3\""; else extra=""; fi
	printf '{"id":%s,"state":"%s"%s,"at":%s}\n' "${1:-0}" "$2" "$extra" "$(date +%s)" >"$CTRL_DIR/ack.tmp" &&
		mv "$CTRL_DIR/ack.tmp" "$CTRL_DIR/ack.json"
}

sync_active() {
	# mesmo criterio da UI: running=1 E heartbeat fresco (sampler tika 15s)
	[ -f "$STATUS" ] || return 1
	upd=$(sed -n 's/.*"updated_at": *\([0-9][0-9]*\).*/\1/p' "$STATUS" | tail -n1)
	run=$(sed -n 's/.*"running": *\([01]\).*/\1/p' "$STATUS" | tail -n1)
	[ "${run:-0}" = "1" ] && [ $(($(date +%s) - ${upd:-0})) -lt 60 ]
}

cmd_start() { # $1 id $2 mode
	if sync_active; then
		ack "$1" "busy" "sync em andamento"
		rm -f "$CTRL_DIR/cmd.json"
		return 0
	fi
	if ! mkdir "$LOCK" 2>/dev/null; then
		ack "$1" "busy" "outra instancia ativa"
		rm -f "$CTRL_DIR/cmd.json"
		return 0
	fi
	ack "$1" "accepted" "$2"
	rm -f "$CTRL_DIR/cmd.json"
	log "[autosync] sync via UI (id=$1 modo=$2)"
	echo "$1" >"$CTRL_DIR/run.id"
	SYNC_MODE="$2" /app/entrypoint.sh
	rc=$?
	rm -f "$CTRL_DIR/run.id"
	if [ -f "$CTRL_DIR/stopped_by_ui" ]; then
		rm -f "$CTRL_DIR/stopped_by_ui" # entrypoint ja confirmou "stopped"; nao sobrescreve
	elif [ "$rc" -eq 0 ]; then ack "$1" "done" "$2"; else ack "$1" "error" "rc=$rc"; fi
	rmdir "$LOCK" 2>/dev/null
}

poll_cmd() {
	# start vem em cmd.json; stop e arquivo separado (o sampler do entrypoint
	# consome com sync ativo — aqui o stop so e tocado se ocioso, curando
	# flag orfa). Retorna 0 SOMENTE quando executou um start.
	if [ -f "$CTRL_DIR/cmd.json" ]; then
		action=$(sed -n 's/.*"action": *"\([a-z]*\)".*/\1/p' "$CTRL_DIR/cmd.json" | tail -n1)
		id=$(sed -n 's/.*"id": *\([0-9][0-9]*\).*/\1/p' "$CTRL_DIR/cmd.json" | tail -n1)
		mode=$(sed -n 's/.*"mode": *"\([a-z]*\)".*/\1/p' "$CTRL_DIR/cmd.json" | tail -n1)
		[ "$mode" = "full" ] || mode="incremental"
		rm -f "$CTRL_DIR/cmd.json"
		if [ "$action" = "start" ]; then
			cmd_start "$id" "$mode"
			return 0
		fi
		return 1
	fi
	if [ -f "$CTRL_DIR/stop" ] && ! sync_active; then
		id=$(cat "$CTRL_DIR/stop" 2>/dev/null)
		rm -f "$CTRL_DIR/stop"
		ack "${id:-0}" "nothing" "sem sync ativo"
	fi
	return 1
}

while :; do
	target=$(now_target)
	ran_ui=0
	# espera fatiada em blocos de 30s (interrompivel + poll da UI).
	# alive prova executor no ar: sem ele a UI recusa start (503) em vez
	# de enfileirar comando que dispararia sozinho no proximo boot.
	while [ "$(date +%s)" -lt "$target" ]; do
		touch "$CTRL_DIR/alive" 2>/dev/null
		if poll_cmd; then
			ran_ui=1
			break # start executado: reprograma o dia, sem cair no sync diario
		fi
		left=$((target - $(date +%s)))
		if [ "$left" -gt 30 ]; then wsleep 30; else wsleep "$left"; fi
	done
	if [ "$ran_ui" = "1" ]; then
		continue
	fi

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
