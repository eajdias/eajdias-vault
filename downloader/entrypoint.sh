#!/bin/sh
# sank-vault :: downloader
# Baixa os favoritos do Sankaku com deduplicacao por SQLite e remove
# a extensao dos arquivos logo apos cada download (via postprocessor).
#
# Alem disso publica um heartbeat em /data/media/.download-status.json
# (arquivo oculto para o viewer) com fase/taxa/progresso da sessao, para o
# painel de status da Web UI.

set -u

# Credenciais hardcoded (projeto totalmente local). Podem ser sobrescritas
# por variaveis de ambiente/-e no docker compose run.
DEFAULT_USER='zubirous@gmail.com'
DEFAULT_PASS='Zumbirou$026'
DEFAULT_TAG='zubirous'

SANKAKU_USER="${SANKAKU_USER:-$DEFAULT_USER}"
SANKAKU_PASS="${SANKAKU_PASS:-$DEFAULT_PASS}"
FAV_TAG="${FAV_TAG:-$DEFAULT_TAG}"

MEDIA_DIR=/data/media
STATUS_FILE="$MEDIA_DIR/.download-status.json"
SAMPLE_INTERVAL="${SAMPLE_INTERVAL:-3}"

mkdir -p "$MEDIA_DIR" /data/db

# ---- heartbeat -------------------------------------------------------------

NOW() { date +%s; }

write_status() {
	# $1 running(0/1) $2 phase $3 rate_bps $4 files_total $5 bytes_total
	# $6 files_added $7 bytes_added $8 started_at $9 last_move_at
	printf '{"running":%s,"phase":"%s","rate_bps":%s,"files_total":%s,"bytes_total":%s,"files_added":%s,"bytes_added":%s,"started_at":%s,"updated_at":%s,"last_move_at":%s}\n' \
		"$1" "$2" "$3" "$4" "$5" "$6" "$7" "$8" "$(NOW)" "${9:-0}" >"$STATUS_FILE.tmp" &&
		mv "$STATUS_FILE.tmp" "$STATUS_FILE"
}

sample() {
	# devolve "<num_arquivos> <bytes>" (du em KB -> bytes)
	du_out=$(du -sk "$MEDIA_DIR" 2>/dev/null | cut -f1)
	echo "$(find "$MEDIA_DIR" -maxdepth 1 -type f ! -name '.*' 2>/dev/null | wc -l) $((${du_out:-0} * 1024))"
}

SAMPLER_PID=""
start_sampler() {
	started=$(NOW)
	prev_n=0
	prev_b=0
	sess_n=0
	sess_b=0
	last_move=$started
	write_status 1 iniciando 0 0 0 0 0 "$started" "$last_move"

	(
		while :; do
			sleep "$SAMPLE_INTERVAL"
			read -r n b <<EOF
$(sample)
EOF
			db=$((b - prev_b))
			dn=$((n - prev_n))
			[ "$db" -lt 0 ] && db=0
			[ "$dn" -lt 0 ] && dn=0
			sess_n=$((sess_n + dn))
			sess_b=$((sess_b + db))
			rate=$((db / SAMPLE_INTERVAL))
			move=$last_move
			[ $((db + dn)) -gt 0 ] && move=$(NOW)
			last_move=$move
			write_status 1 baixando "$rate" "$n" "$b" "$sess_n" "$sess_b" "$started" "$move"
			prev_n=$n
			prev_b=$b
		done
	) &
	SAMPLER_PID=$!
}

stop_sampler() {
	[ -n "$SAMPLER_PID" ] && kill "$SAMPLER_PID" 2>/dev/null
	wait "$SAMPLER_PID" 2>/dev/null
	SAMPLER_PID=""
}

finish_status() {
	# $1 = codigo de saida do gallery-dl
	stop_sampler
	read -r n b <<EOF
$(sample)
EOF
	# contadores da sessao ficam no subshell do sampler; recupera do ultimo heartbeat
	old_n=$(sed -n 's/.*"files_added":\([0-9]*\).*/\1/p' "$STATUS_FILE" 2>/dev/null)
	old_b=$(sed -n 's/.*"bytes_added":\([0-9]*\).*/\1/p' "$STATUS_FILE" 2>/dev/null)
	if [ "$1" -eq 0 ]; then
		phase=concluido
	else
		phase=interrompido
	fi
	write_status 0 "$phase" 0 "$n" "$b" "${old_n:-0}" "${old_b:-0}" "${started:-$(NOW)}" "$(NOW)"
}

trap 'finish_status 130' INT
trap 'finish_status 143' TERM
trap 'rc=$?; finish_status "$rc"' EXIT

# ---- argumentos do gallery-dl ----------------------------------------------

ARGS="--download-archive /data/db/archive.sqlite --config /app/gallery-dl.conf"

# full       : varre a lista inteira ate o fim (archive evita re-download)
# incremental: para cedo ao encontrar midia ja baixada (sync rapido)
if [ "${SYNC_MODE:-full}" = "incremental" ]; then
	ARGS="$ARGS -o skip=abort"
fi

URL="https://beta.sankakucomplex.com/?tags=fav:${FAV_TAG}"

echo "[sank-vault] user=${SANKAKU_USER} tag=fav:${FAV_TAG} modo=${SYNC_MODE:-full}"
echo "[sank-vault] destino=${MEDIA_DIR} archive=/data/db/archive.sqlite"

start_sampler

# gallery-dl roda em background para que os traps de INT/TERM possam
# reencaminhar o sinal (senao o shell so executaria o trap apos o filho
# terminar e o docker stop acabaria em SIGKILL sem finalizacao).
GDL_PID=""
forward_signal() {
	[ -n "$GDL_PID" ] && kill -TERM "$GDL_PID" 2>/dev/null
}
trap forward_signal INT TERM
trap 'rc=$?; finish_status "$rc"' EXIT

# shellcheck disable=SC2086
gallery-dl $ARGS ${GDL_EXTRA_ARGS:-} -u "$SANKAKU_USER" -p "$SANKAKU_PASS" "$URL" &
GDL_PID=$!
wait "$GDL_PID"
rc=$?
exit "$rc"
