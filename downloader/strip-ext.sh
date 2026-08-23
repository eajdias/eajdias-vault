#!/bin/sh
# Remove a extensao final de cada arquivo recebido como argumento.
# Usado como postprocessor "exec" do gallery-dl para ocultar o tipo da midia.
for f in "$@"; do
	[ -f "$f" ] || continue
	case "$f" in
	*.*) mv -f -- "$f" "${f%.*}" ;;
	esac
done
