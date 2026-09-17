package main

// gzip.go — compressão das respostas de texto (JSON da API + assets do front)
// para rede celular. Mídia e thumbs já são binários e ficam de fora: o
// middleware é aplicado por rota, não global.

import (
	"compress/gzip"
	"net/http"
	"strings"
)

type gzipWriter struct {
	http.ResponseWriter
	gz *gzip.Writer
}

func (g *gzipWriter) Write(b []byte) (int, error) { return g.gz.Write(b) }

func withGzip(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		next(&gzipWriter{ResponseWriter: w, gz: gz}, r)
	}
}
