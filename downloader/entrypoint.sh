#!/bin/sh
# sank-vault :: downloader
# Baixa os favoritos do Sankaku com deduplicacao por SQLite e remove
# a extensao dos arquivos logo apos cada download (via postprocessor).
set -eu

# Credenciais hardcoded (projeto totalmente local). Podem ser sobrescritas
# por variaveis de ambiente/-e no docker compose run.
DEFAULT_USER='zubirous@gmail.com'
DEFAULT_PASS='Zumbirou$026'
DEFAULT_TAG='zubirous'

SANKAKU_USER="${SANKAKU_USER:-$DEFAULT_USER}"
SANKAKU_PASS="${SANKAKU_PASS:-$DEFAULT_PASS}"
FAV_TAG="${FAV_TAG:-$DEFAULT_TAG}"

mkdir -p /data/media /data/db

ARGS="--download-archive /data/db/archive.sqlite --config /app/gallery-dl.conf"

# full       : varre a lista inteira ate o fim (archive evita re-download)
# incremental: para cedo ao encontrar midia ja baixada (sync rapido)
if [ "${SYNC_MODE:-full}" = "incremental" ]; then
	ARGS="$ARGS -o skip=abort"
fi

URL="https://beta.sankakucomplex.com/?tags=fav:${FAV_TAG}"

echo "[sank-vault] user=${SANKAKU_USER} tag=fav:${FAV_TAG} modo=${SYNC_MODE:-full}"
echo "[sank-vault] destino=/data/media archive=/data/db/archive.sqlite"
# shellcheck disable=SC2086
exec gallery-dl $ARGS ${GDL_EXTRA_ARGS:-} -u "$SANKAKU_USER" -p "$SANKAKU_PASS" "$URL"
