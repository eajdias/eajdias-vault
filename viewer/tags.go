package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

// tags.go — índice de tags em SQLite com cache quente em memória.
//
// O JSON estourou o teto (~10MB em vault grande: reescrita integral a cada
// edição + parse integral no boot). O desenho continua o mesmo — mapa em
// memória para leitura quente — mas a persistência é write-through por linha:
// edição = 1 UPDATE, nunca reescrita do arquivo. As listas por mídia ficam
// como JSON na coluna (só lidas inteiras; normalizar em linhas seria
// over-engineering para a semântica atual de filtro AND em Go).

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

const tagSchema = `CREATE TABLE IF NOT EXISTS tags (
	name TEXT PRIMARY KEY,
	tags TEXT NOT NULL DEFAULT '[]',
	custom TEXT NOT NULL DEFAULT '[]'
);`

type tagStore struct {
	mu   sync.Mutex
	db   *sql.DB
	data map[string]*tagEntry // cache quente; o SQLite é a verdade persistente

	freqCache map[string]int
	freqDirty bool
}

func newTagStore(dbPath string) *tagStore {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("[tags] open %s: %v", dbPath, err)
	}
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode=WAL;`).Scan(&mode); err != nil {
		log.Fatalf("[tags] WAL: %v", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		log.Fatalf("[tags] busy_timeout: %v", err)
	}
	if _, err := db.Exec(tagSchema); err != nil {
		log.Fatalf("[tags] schema: %v", err)
	}
	ts := &tagStore{db: db, data: map[string]*tagEntry{}, freqDirty: true}
	rows, err := db.Query(`SELECT name, tags, custom FROM tags`)
	if err != nil {
		log.Fatalf("[tags] load: %v", err)
	}
	for rows.Next() {
		var name, tj, cj string
		if err := rows.Scan(&name, &tj, &cj); err != nil {
			continue
		}
		e := &tagEntry{}
		_ = json.Unmarshal([]byte(tj), &e.Tags)
		_ = json.Unmarshal([]byte(cj), &e.Custom)
		for i, tg := range e.Tags {
			e.Tags[i] = normTag(tg)
		}
		for i, tg := range e.Custom {
			e.Custom[i] = normTag(tg)
		}
		ts.data[name] = e
	}
	rows.Close()
	return ts
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Close libera o banco (testes; o servidor vive até o processo morrer).
func (t *tagStore) Close() error { return t.db.Close() }

// upsert grava a linha (chamadas seguram t.mu).
func (t *tagStore) upsert(name string) {
	e := t.data[name]
	tj, _ := json.Marshal(nonNilStrings(e.Tags))
	cj, _ := json.Marshal(nonNilStrings(e.Custom))
	if _, err := t.db.Exec(`INSERT INTO tags(name,tags,custom) VALUES(?,?,?)
		ON CONFLICT(name) DO UPDATE SET tags=excluded.tags, custom=excluded.custom`,
		name, string(tj), string(cj)); err != nil {
		log.Printf("[tags] upsert %s: %v", name, err)
	}
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
	changed := false
	for _, tg := range tags {
		tg = normTag(tg)
		if tg == "" || seen[tg] {
			continue
		}
		seen[tg] = true
		e.Tags = append(e.Tags, tg)
		changed = true
	}
	if changed {
		t.freqDirty = true
		t.upsert(name)
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
// Resultado é cacheado e invalidado a cada mudança: o autocomplete dispara
// por tecla e o scan integral não precisa se repetir sem mudança.
func (t *tagStore) freq() map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.freqDirty && t.freqCache != nil {
		return copyFreq(t.freqCache)
	}
	f := map[string]int{}
	for _, e := range t.data {
		for _, tg := range e.Tags {
			f[tg]++
		}
		for _, tg := range e.Custom {
			f[tg]++
		}
	}
	t.freqCache, t.freqDirty = f, false
	return copyFreq(f)
}

func copyFreq(f map[string]int) map[string]int {
	out := make(map[string]int, len(f))
	for k, v := range f {
		out[k] = v
	}
	return out
}

// editCustom adiciona/remove customs (normalizados, sem duplicatas),
// cria a entrada se ausente, persiste e devolve cópia do estado final.
func (t *tagStore) editCustom(name string, add, del []string) *tagEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
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
	t.freqDirty = true
	t.upsert(name)
	cp := &tagEntry{}
	cp.Tags = append([]string{}, e.Tags...)
	cp.Custom = append([]string{}, e.Custom...)
	return cp
}

// prune remove entradas de mídias que não existem mais; devolve se mudou algo.
func (t *tagStore) prune(alive map[string]bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	var stale []string
	for k := range t.data {
		if !alive[k] {
			delete(t.data, k)
			stale = append(stale, k)
		}
	}
	if len(stale) == 0 {
		return false
	}
	for i := 0; i < len(stale); i += 500 {
		end := i + 500
		if end > len(stale) {
			end = len(stale)
		}
		chunk := stale[i:end]
		q := `DELETE FROM tags WHERE name IN (?` + strings.Repeat(",?", len(chunk)-1) + `)`
		args := make([]any, len(chunk))
		for j, n := range chunk {
			args[j] = n
		}
		if _, err := t.db.Exec(q, args...); err != nil {
			log.Printf("[tags] prune: %v", err)
		}
	}
	t.freqDirty = true
	return true
}
