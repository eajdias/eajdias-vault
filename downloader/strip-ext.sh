#!/bin/sh
# Remove a extensao final de cada arquivo recebido como argumento.
# Usado como postprocessor "exec" do gallery-dl para ocultar o tipo da midia.
# Tambem recolhe o sidecar de tags ({midia}.json -> .meta/): conteudo
# identificavel, fora do alcance de olhada casual e de indexadores.
for f in "$@"; do
	[ -f "$f" ] || continue
	case "$f" in
	*.json) continue ;;
	*.*)
		stripped="${f%.*}"
		mv -f -- "$f" "$stripped"
		if [ -f "$stripped.json" ]; then
			mkdir -p "$(dirname "$stripped")/.meta"
			mv -f -- "$stripped.json" "$(dirname "$stripped")/.meta/"
		fi
		;;
	esac
done
