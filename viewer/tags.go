package main

import (
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"
)

// tagEntry é o registro de uma mídia no índice de tags.
// Tags vem da fonte (Sankaku, via sidecar); Custom é do usuário
// e nunca é sobrescrito pela ingestão.
type tagEntry struct {
	Tags   []string `json:"tags"`
	Custom []string `json:"custom,omitempty"`
}

// normTag é a normalização canônica: minúsculo, espaços→"_", trim.
func normTag(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), " ", "_")
}

type tagStore struct {
	mu    sync.Mutex
	path  string
	data  map[string]*tagEntry
	dirty bool
}

func newTagStore(path string) *tagStore {
	ts := &tagStore{path: path, data: map[string]*tagEntry{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &ts.data)
		if ts.data == nil {
			ts.data = map[string]*tagEntry{}
		}
		for _, e := range ts.data {
			for i, tg := range e.Tags {
				e.Tags[i] = normTag(tg)
			}
			for i, tg := range e.Custom {
				e.Custom[i] = normTag(tg)
			}
		}
	} else if !os.IsNotExist(err) {
		log.Printf("[tags] não consegui ler %s: %v", path, err)
	}
	return ts
}

// ingest cria a entrada se ausente e mescla tags da fonte (deduplicadas,
// normalizadas). Customs existentes são preservados.
func (t *tagStore) ingest(name string, tags []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.data[name]
	if !ok {
		if len(tags) == 0 {
			return // sem sidecar ainda: tenta de novo no próximo rescan
		}
		e = &tagEntry{}
		t.data[name] = e
	}
	seen := map[string]bool{}
	for _, tg := range e.Tags {
		seen[tg] = true
	}
	for _, tg := range tags {
		tg = normTag(tg)
		if tg == "" || seen[tg] {
			continue
		}
		seen[tg] = true
		e.Tags = append(e.Tags, tg)
		t.dirty = true
	}
}

// get devolve cópia da entrada (nil se ausente).
func (t *tagStore) get(name string) *tagEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.data[name]
	if !ok {
		return nil
	}
	cp := &tagEntry{}
	cp.Tags = append([]string{}, e.Tags...)
	cp.Custom = append([]string{}, e.Custom...)
	return cp
}

// freq conta ocorrências de cada tag (fonte+custom) para autocomplete.
func (t *tagStore) freq() map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	f := map[string]int{}
	for _, e := range t.data {
		for _, tg := range e.Tags {
			f[tg]++
		}
		for _, tg := range e.Custom {
			f[tg]++
		}
	}
	return f
}

// save persiste atomicamente (tmp + rename).
func (t *tagStore) save() {
	t.mu.Lock()
	b, err := json.Marshal(t.data)
	t.mu.Unlock()
	if err != nil {
		log.Printf("[tags] marshal: %v", err)
		return
	}
	tmp := t.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		log.Printf("[tags] write: %v", err)
		return
	}
	_ = os.Rename(tmp, t.path)
}

// editCustom adiciona/remove customs (normalizados, sem duplicatas),
// cria a entrada se ausente, persiste e devolve cópia do estado final.
func (t *tagStore) editCustom(name string, add, del []string) *tagEntry {
	t.mu.Lock()
	e, ok := t.data[name]
	if !ok {
		e = &tagEntry{}
		t.data[name] = e
	}
	drop := map[string]bool{}
	for _, tg := range del {
		if tg = normTag(tg); tg != "" {
			drop[tg] = true
		}
	}
	kept := e.Custom[:0]
	seen := map[string]bool{}
	for _, tg := range e.Custom {
		if !drop[tg] && !seen[tg] {
			seen[tg] = true
			kept = append(kept, tg)
		}
	}
	for _, tg := range add {
		if tg = normTag(tg); tg != "" && !seen[tg] {
			seen[tg] = true
			kept = append(kept, tg)
		}
	}
	e.Custom = kept
	cp := &tagEntry{}
	cp.Tags = append([]string{}, e.Tags...)
	cp.Custom = append([]string{}, e.Custom...)
	t.mu.Unlock()
	t.save()
	return cp
}

// saveIfDirty persiste só quando houve mudança desde o último save.
func (t *tagStore) saveIfDirty() {
	t.mu.Lock()
	if !t.dirty {
		t.mu.Unlock()
		return
	}
	t.dirty = false
	t.mu.Unlock()
	t.save()
}

// prune remove entradas de mídias que não existem mais; devolve se mudou algo.
func (t *tagStore) prune(alive map[string]bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	rm := false
	for k := range t.data {
		if !alive[k] {
			delete(t.data, k)
			rm = true
		}
	}
	if rm {
		t.dirty = true
	}
	return rm
}
