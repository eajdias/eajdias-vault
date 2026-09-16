package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileCacheHitMiss(t *testing.T) {
	fc := newFileCache(filepath.Join(t.TempDir(), "filecache.json"))
	if _, ok := fc.lookup("a", 10, 100); ok {
		t.Fatal("miss esperado em cache vazio")
	}
	fc.store("a", "image/png", 10, 100)
	if mime, ok := fc.lookup("a", 10, 100); !ok || mime != "image/png" {
		t.Fatalf("hit esperado, mime=%q ok=%v", mime, ok)
	}
	if _, ok := fc.lookup("a", 11, 100); ok {
		t.Error("mudanca de tamanho deve invalidar")
	}
	if _, ok := fc.lookup("a", 10, 101); ok {
		t.Error("mudanca de mtime deve invalidar")
	}
}

func TestFileCacheSaveIfDirty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "filecache.json")
	fc := newFileCache(path)
	fc.saveIfDirty()
	if _, err := os.ReadFile(path); err == nil {
		t.Fatal("save sem mudanca nao deve criar arquivo")
	}
	fc.store("a", "video/mp4", 10, 100)
	fc.saveIfDirty()
	fc2 := newFileCache(path)
	if mime, ok := fc2.lookup("a", 10, 100); !ok || mime != "video/mp4" {
		t.Fatalf("reload falhou, mime=%q ok=%v", mime, ok)
	}
	if !fc2.prune(map[string]bool{}) {
		t.Fatal("prune deveria reportar remocao")
	}
	if fc2.prune(map[string]bool{}) {
		t.Fatal("prune sem mudanca deveria reportar false")
	}
}
