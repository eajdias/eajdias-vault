package main

import (
	"encoding/json"
	"log"
	"os"
	"sync"
)

// filecache.go — cache de {tamanho, mtime, mime} por mídia.
//
// As mídias são imutáveis (gallery-dl escreve uma vez), então o rescan valida
// pelo stat (grátis no WalkDir) e só abre+sniffa arquivos novos ou alterados —
// em vez de abrir TODOS a cada 8s. Mora em /data/thumbs/filecache.json.

type fileMeta struct {
	Size  int64  `json:"sz"`
	MTime int64  `json:"mt"`
	MIME  string `json:"mime"`
}

type fileCache struct {
	mu    sync.Mutex
	path  string
	data  map[string]fileMeta
	dirty bool
}

func newFileCache(path string) *fileCache {
	fc := &fileCache{path: path, data: map[string]fileMeta{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &fc.data)
		if fc.data == nil {
			fc.data = map[string]fileMeta{}
		}
	} else if !os.IsNotExist(err) {
		log.Printf("[fcache] não consegui ler %s: %v", path, err)
	}
	return fc
}

// lookup devolve o mime em cache se tamanho+mtime baterem.
func (c *fileCache) lookup(name string, size, mtime int64) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.data[name]
	if !ok || m.Size != size || m.MTime != mtime {
		return "", false
	}
	return m.MIME, true
}

// store registra (ou atualiza) e marca dirty.
func (c *fileCache) store(name, mime string, size, mtime int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m, ok := c.data[name]; ok && m.MIME == mime && m.Size == size && m.MTime == mtime {
		return
	}
	c.data[name] = fileMeta{Size: size, MTime: mtime, MIME: mime}
	c.dirty = true
}

// prune remove entradas de mídias que não existem mais; devolve se mudou algo.
func (c *fileCache) prune(alive map[string]bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	rm := false
	for k := range c.data {
		if !alive[k] {
			delete(c.data, k)
			rm = true
		}
	}
	if rm {
		c.dirty = true
	}
	return rm
}

// saveIfDirty persiste atomicamente só quando houve mudança.
func (c *fileCache) saveIfDirty() {
	c.mu.Lock()
	if !c.dirty {
		c.mu.Unlock()
		return
	}
	b, err := json.Marshal(c.data)
	if err != nil {
		c.mu.Unlock()
		log.Printf("[fcache] marshal: %v", err)
		return
	}
	c.dirty = false
	c.mu.Unlock()
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		log.Printf("[fcache] write: %v", err)
		return
	}
	_ = os.Rename(tmp, c.path)
}
