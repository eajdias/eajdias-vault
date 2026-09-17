package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithGzip(t *testing.T) {
	h := withGzip(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"a":1}`))
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("sem Content-Encoding gzip: %v", rec.Header())
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(zr)
	zr.Close()
	if string(body) != `{"a":1}` {
		t.Fatalf("corpo diverge: %q", body)
	}

	// sem Accept-Encoding: passthrough sem tocar
	req2 := httptest.NewRequest("GET", "/", nil)
	rec2 := httptest.NewRecorder()
	h(rec2, req2)
	if enc := rec2.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("comprimiu sem pedir: %q", enc)
	}
	if rec2.Body.String() != `{"a":1}` {
		t.Fatalf("passthrough diverge: %q", rec2.Body.String())
	}
}
