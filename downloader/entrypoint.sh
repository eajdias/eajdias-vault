#!/bin/sh
# zub-vault :: downloader
# Baixa os favoritos do Sankaku com deduplicacao por SQLite e remove
# a extensao dos arquivos logo apos cada download (via postprocessor).
#
# Alem disso publica um heartbeat em /data/media/.download-status.json
# (arquivo oculto para o viewer) com fase/taxa/progresso da sessao, para o
# painel de status da Web UI.

set -u

# opacidade: tudo criado aqui nasce 640/750 com grupo vault (gid 2000) — o
# viewer (membro do grupo) le, o desktop (fora do grupo, sem sudo) nao.
# setgid nos diretorios: arquivos novos herdam o grupo sozinhos.
umask 027

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
MODE="${SYNC_MODE:-full}"

mkdir -p "$MEDIA_DIR" /data/db

# controle via UI (sem socket docker): a UI escreve cmd/stop, o autosync e
# este sampler consomem. dir 2770 root:vault — o viewer (membro do grupo)
# escreve comandos, nunca executa nada.
CTRL_DIR="/data/db/.uictrl"
mkdir -p "$CTRL_DIR"
chgrp 2000 "$CTRL_DIR" 2>/dev/null
chmod 2770 "$CTRL_DIR" 2>/dev/null

# sidecars de tags moram em .meta/ (oculto): recolhe legados da raiz e
# sinaliza indexadores de midia (Android/Plex) para ignorarem o vault.
# converge grupo+perms (acervo antigo, one-shots manuais sem umask): no estado
# estavel os finds abaixo nao tocam em nada (zero fork).
mkdir -p "$MEDIA_DIR/.meta"
chmod 2750 "$MEDIA_DIR" "$MEDIA_DIR/.meta" 2>/dev/null
chgrp 2000 "$MEDIA_DIR" "$MEDIA_DIR/.meta" 2>/dev/null
find "$MEDIA_DIR" -maxdepth 2 ! -group 2000 -exec chgrp 2000 {} + 2>/dev/null
find "$MEDIA_DIR" -maxdepth 2 -type f ! -perm 640 -exec chmod 640 {} + 2>/dev/null
find "$MEDIA_DIR" -maxdepth 1 -type d ! -perm 2750 -exec chmod 2750 {} + 2>/dev/null
for legacy in "$MEDIA_DIR"/*.json; do
	[ -f "$legacy" ] || break
	mv -f -- "$legacy" "$MEDIA_DIR/.meta/"
done
touch "$MEDIA_DIR/.nomedia"

# ---- heartbeat -------------------------------------------------------------

NOW() { date +%s; }

write_status() {
	# $1 running(0/1) $2 phase $3 rate_bps $4 files_total $5 bytes_total
	# $6 files_added $7 bytes_added $8 started_at $9 last_move_at
	printf '{"running":%s,"phase":"%s","rate_bps":%s,"files_total":%s,"bytes_total":%s,"files_added":%s,"bytes_added":%s,"started_at":%s,"updated_at":%s,"last_move_at":%s,"mode":"%s"}\n' \
		"$1" "$2" "$3" "$4" "$5" "$6" "$7" "$8" "$(NOW)" "${9:-0}" "$MODE" >"$STATUS_FILE.tmp" &&
		mv "$STATUS_FILE.tmp" "$STATUS_FILE"
}

sample() {
	# devolve "<num_arquivos> <bytes>" (du em KB -> bytes; sidecars de tags
	# em .meta/ nao contam como midias do vault)
	du_out=$(du -sk "$MEDIA_DIR" 2>/dev/null | cut -f1)
	echo "$(find "$MEDIA_DIR" -maxdepth 1 -type f ! -name '.*' ! -name '*.json' 2>/dev/null | wc -l) $((${du_out:-0} * 1024))"
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
			check_stop
		done
	) &
	SAMPLER_PID=$!
}

# check_stop consome o pedido de parada da UI (um por tick do sampler):
# mata o gallery-dl com TERM (finalizacao graciosa via traps) e confirma
# em ack.json. Guarda /proc evita matar pid reciclado.
check_stop() {
	[ -f "$CTRL_DIR/stop" ] || return 0
	id=$(cat "$CTRL_DIR/stop" 2>/dev/null)
	rm -f "$CTRL_DIR/stop"
	pid=$(cat "$CTRL_DIR/gdl.pid" 2>/dev/null)
	state="nothing"
	if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
		case "$(tr '\0' ' ' </proc/$pid/cmdline 2>/dev/null)" in
		*gallery-dl*)
			kill -TERM "$pid" 2>/dev/null
			state="stopped"
			touch "$CTRL_DIR/stopped_by_ui" # flag p/ finish_status fechar o start com "stopped"
			;;
		esac
	fi
	if [ "$state" = "nothing" ]; then
		detail=',"detail":"sem sync ativo"'
	else
		detail=""
	fi
	printf '{"id":%s,"state":"%s"%s,"at":%s}\n' "${id:-0}" "$state" "$detail" "$(NOW)" >"$CTRL_DIR/ack.tmp" &&
		mv "$CTRL_DIR/ack.tmp" "$CTRL_DIR/ack.json"
}

stop_sampler() {
	[ -n "$SAMPLER_PID" ] && kill "$SAMPLER_PID" 2>/dev/null
	wait "$SAMPLER_PID" 2>/dev/null
	SAMPLER_PID=""
}

finish_status() {
	# $1 = codigo de saida do gallery-dl
	stop_sampler
	rm -f "$CTRL_DIR/gdl.pid"
	# parada via UI: fecha o comando start como "stopped" (em vez de erro
	# rc=143); o autosync ve o marker e nao sobrescreve
	if [ -f "$CTRL_DIR/stopped_by_ui" ] && [ -f "$CTRL_DIR/run.id" ]; then
		id=$(cat "$CTRL_DIR/run.id" 2>/dev/null)
		rm -f "$CTRL_DIR/run.id"
		printf '{"id":%s,"state":"stopped","at":%s}\n' "${id:-0}" "$(NOW)" >"$CTRL_DIR/ack.tmp" &&
			mv "$CTRL_DIR/ack.tmp" "$CTRL_DIR/ack.json"
	fi
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
# incremental: pula o ja baixado e para apos 5 skips SEGUIDOS (fim real da
#              lista tambem para sozinho com rc=0)
if [ "${SYNC_MODE:-full}" = "incremental" ]; then
	ARGS="$ARGS -o skip=abort:5"
fi

URL="https://beta.sankakucomplex.com/?tags=fav:${FAV_TAG}"

echo "[zub-vault] user=${SANKAKU_USER} tag=fav:${FAV_TAG} modo=${SYNC_MODE:-full}"
echo "[zub-vault] destino=${MEDIA_DIR} archive=/data/db/archive.sqlite"

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
echo "$GDL_PID" >"$CTRL_DIR/gdl.pid"
wait "$GDL_PID"
rc=$?
exit "$rc"
