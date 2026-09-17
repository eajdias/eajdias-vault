#!/bin/sh
# Backfill one-shot: para cada midia /data/media/{id}_{md5} sem sidecar,
# busca os metadados do post e grava .meta/{id}_{md5}.json com as tags.
# Idempotente: pula quem ja tem sidecar. Throttle de 2s entre posts.
# Uso: docker compose run --rm --entrypoint /app/backfill-tags.sh downloader [N]
set -u
SANKAKU_USER="${SANKAKU_USER:-zubirous@gmail.com}"
SANKAKU_PASS="${SANKAKU_PASS:-Zumbirou\$026}"
MEDIA_DIR=/data/media
META_DIR="$MEDIA_DIR/.meta"
LIMIT="${1:-0}"
mkdir -p "$META_DIR"
n=0
for f in "$MEDIA_DIR"/*; do
	[ -f "$f" ] || continue
	name=$(basename "$f")
	case "$name" in .*|*.json|*.part) continue ;; esac
	[ -f "$META_DIR/$name.json" ] && continue
	id=${name%%_*}
	[ -n "$id" ] || continue
	if gallery-dl -j -u "$SANKAKU_USER" -p "$SANKAKU_PASS" \
		"https://sankaku.app/posts/show/$id" 2>/dev/null \
		| python3 -c 'import json,sys
def find(o):
    if isinstance(o, dict):
        if isinstance(o.get("tags"), list) and o["tags"]:
            return o["tags"]
        if isinstance(o.get("tag_names"), list) and o["tag_names"]:
            return o["tag_names"]
        for v in o.values():
            r = find(v)
            if r is not None:
                return r
    elif isinstance(o, list):
        for v in o:
            r = find(v)
            if r is not None:
                return r
    return None
ts = find(json.load(sys.stdin))
if not ts:
    sys.exit(1)
print(json.dumps({"tags": ts}))' >"$META_DIR/$name.json.tmp"; then
		mv "$META_DIR/$name.json.tmp" "$META_DIR/$name.json"
		n=$((n + 1))
		echo "[$n] $name ok"
	else
		rm -f "$META_DIR/$name.json.tmp"
		echo "[$n] $name FALHOU (mantido p/ retry)" >&2
	fi
	sleep 2
	if [ "$LIMIT" -gt 0 ] && [ "$n" -ge "$LIMIT" ]; then break; fi
done
echo "backfill concluido: $n sidecars"
